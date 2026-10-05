package internal

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	backupv1 "github.com/Muxcore-Media/backup-local/muxcore/backup/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fakeSQLiteFile returns bytes shaped like a SQLite main database: the 16-byte
// magic header, page size 4096 big-endian, then deterministic page content.
// No SQLite driver is a dependency of this module (and adding one for a test
// is out of scope), so the drill compares bytes and header structure instead
// of running PRAGMA integrity_check.
func fakeSQLiteFile(pages int) []byte {
	b := make([]byte, pages*4096)
	copy(b, "SQLite format 3\x00")
	b[16], b[17] = 0x10, 0x00 // page size 4096
	for i := 100; i < len(b); i++ {
		b[i] = byte(i * 31)
	}
	return b
}

func TestRestoreDrillFreshInstall(t *testing.T) {
	ctx := context.Background()

	src := t.TempDir()
	dbBytes := fakeSQLiteFile(4)
	walBytes := append([]byte{0x37, 0x7f, 0x06, 0x82}, bytes.Repeat([]byte("wal-frame"), 600)...)
	if err := os.WriteFile(filepath.Join(src, "app.db"), dbBytes, 0600); err != nil {
		t.Fatal(err)
	}
	// WAL present and not checkpointed: the archive must carry it so a restore
	// replays committed rows.
	if err := os.WriteFile(filepath.Join(src, "app.db-wal"), walBytes, 0600); err != nil {
		t.Fatal(err)
	}

	m1 := testModule(t)
	created, err := m1.CreateBackup(ctx, &backupv1.CreateBackupRequest{SourcePaths: []string{src}})
	if err != nil {
		t.Fatal(err)
	}
	id := created.GetBackup().GetId()
	ver, err := m1.VerifyBackup(ctx, &backupv1.VerifyBackupRequest{BackupId: id, RestoreTest: true})
	if err != nil {
		t.Fatal(err)
	}
	if !ver.GetValid() || !ver.GetRestoreTestOk() || ver.GetFilesInArchive() != 2 {
		t.Fatalf("verify: %+v", ver)
	}

	// Fresh install: new dir containing only the archive, no index.json.
	fresh := t.TempDir()
	archiveName := id + ".tar.gz"
	data, err := os.ReadFile(archivePath(m1.dir, id))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fresh, archiveName), data, 0600); err != nil {
		t.Fatal(err)
	}
	m2 := NewModule(Config{Dir: fresh, GRPCAddr: "127.0.0.1:0", HTTPAddr: "127.0.0.1:0"})

	// Restore without Init: unknown ID triggers the rescan.
	target := filepath.Join(t.TempDir(), "restored")
	resp, err := m2.RestoreBackup(ctx, &backupv1.RestoreBackupRequest{BackupId: id, TargetPath: target})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetFilesRestored() != 2 {
		t.Fatalf("files restored = %d", resp.GetFilesRestored())
	}
	base := filepath.Base(src)
	gotDB, err := os.ReadFile(filepath.Join(target, "data", base, "app.db"))
	if err != nil || !bytes.Equal(gotDB, dbBytes) {
		t.Fatalf("restored db mismatch: %v", err)
	}
	if !bytes.HasPrefix(gotDB, []byte("SQLite format 3\x00")) {
		t.Fatal("restored db lost SQLite header")
	}
	gotWAL, err := os.ReadFile(filepath.Join(target, "data", base, "app.db-wal"))
	if err != nil || !bytes.Equal(gotWAL, walBytes) {
		t.Fatalf("restored wal mismatch: %v", err)
	}

	// The rebuilt index was persisted and marks the entry recovered.
	raw, err := os.ReadFile(filepath.Join(fresh, "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	var list []backupMeta
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if len(list) != 1 || !list[0].Recovered || list[0].ID != id ||
		list[0].Checksum != hex.EncodeToString(sum[:]) || list[0].Size != int64(len(data)) {
		t.Fatalf("index = %+v", list)
	}
	if list[0].Timestamp != created.GetBackup().GetTimestampUnix() && list[0].Timestamp == 0 {
		t.Fatalf("timestamp not derived: %+v", list[0])
	}
}

func TestInitRebuildsIndexAndListVerify(t *testing.T) {
	ctx := context.Background()
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "f.txt"), []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}
	peer := &memPeer{data: []byte("state")}
	m1 := testModule(t, BackupablePeer{ID: "mod-a", Backend: peer})
	created, err := m1.CreateBackup(ctx, &backupv1.CreateBackupRequest{SourcePaths: []string{src}})
	if err != nil {
		t.Fatal(err)
	}
	id := created.GetBackup().GetId()

	fresh := t.TempDir()
	data, _ := os.ReadFile(archivePath(m1.dir, id))
	_ = os.WriteFile(filepath.Join(fresh, id+".tar.gz"), data, 0600)
	// Junk that must be ignored: wrong shapes, symlink, traversal-ish name.
	_ = os.WriteFile(filepath.Join(fresh, "backup_abc.tar.gz"), data, 0600)
	_ = os.WriteFile(filepath.Join(fresh, "backup_1..tar.gz"), data, 0600)
	_ = os.Symlink(filepath.Join(fresh, id+".tar.gz"), filepath.Join(fresh, "backup_99.tar.gz"))
	_ = os.WriteFile(filepath.Join(fresh, "backup_7.tar.gz"), []byte("not gzip"), 0600)

	m2 := NewModule(Config{Dir: fresh, GRPCAddr: "127.0.0.1:0", HTTPAddr: "127.0.0.1:0"})
	if err := m2.Init(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m2.grpcLis.Close(); _ = m2.httpLis.Close() })

	lst, err := m2.ListBackups(ctx, &backupv1.ListBackupsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(lst.GetBackups()) != 1 || lst.GetBackups()[0].GetId() != id {
		t.Fatalf("list = %+v", lst.GetBackups())
	}
	if got := lst.GetBackups()[0].GetModuleIds(); len(got) != 1 || got[0] != "mod-a" {
		t.Fatalf("module ids = %v", got)
	}
	v, err := m2.VerifyBackup(ctx, &backupv1.VerifyBackupRequest{BackupId: id, RestoreTest: true})
	if err != nil || !v.GetValid() {
		t.Fatalf("verify: %+v %v", v, err)
	}

	// Unknown / hostile IDs still NotFound.
	for _, bad := range []string{"backup_1", "../" + id, "backup_abc"} {
		_, err := m2.RestoreBackup(ctx, &backupv1.RestoreBackupRequest{BackupId: bad, TargetPath: t.TempDir()})
		if status.Code(err) != codes.NotFound {
			t.Errorf("id %q: code = %v", bad, status.Code(err))
		}
	}
}
