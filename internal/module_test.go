package internal

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"testing"

	backupv1 "github.com/Muxcore-Media/backup-local/muxcore/backup/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type memPeer struct {
	data []byte
}

func (p *memPeer) ExportState(ctx context.Context) ([]byte, error) {
	out := make([]byte, len(p.data))
	copy(out, p.data)
	return out, nil
}

func (p *memPeer) ImportState(ctx context.Context, data []byte) error {
	p.data = append([]byte(nil), data...)
	return nil
}

func testModule(t *testing.T, peers ...BackupablePeer) *Module {
	t.Helper()
	t.Setenv("BACKUP_DIR", "")
	t.Setenv("BACKUP_SOURCE_DIRS", "")
	dir := t.TempDir()
	m := NewModule(Config{
		Dir:      dir,
		GRPCAddr: "127.0.0.1:0",
		HTTPAddr: "127.0.0.1:0",
		Peers:    peers,
	})
	return m
}

func TestModuleInfo(t *testing.T) {
	m := testModule(t)
	info := m.Info()
	if info.ID == "" {
		t.Error("module ID must not be empty")
	}
	if info.Version == "" {
		t.Error("module version must not be empty")
	}
}

func TestModuleLifecycle(t *testing.T) {
	m := testModule(t)
	ctx := context.Background()
	if err := m.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := m.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := m.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestBackupablePeerOnlyRoundTrip(t *testing.T) {
	peer := &memPeer{data: []byte("peer-only-state")}
	m := testModule(t, BackupablePeer{ID: "exporter-a", Backend: peer})
	ctx := context.Background()

	created, err := m.CreateBackup(ctx, &backupv1.CreateBackupRequest{
		ModuleIds: []string{"exporter-a"},
	})
	if err != nil {
		t.Fatalf("CreateBackup (peer only): %v", err)
	}
	info := created.GetBackup()
	if info.GetSizeBytes() <= 0 {
		t.Fatalf("expected non-empty archive from Backupable peer, got %+v", info)
	}
	if len(info.GetModuleIds()) != 1 || info.GetModuleIds()[0] != "exporter-a" {
		t.Fatalf("module ids: %v", info.GetModuleIds())
	}

	peer.data = []byte("wiped")
	target := t.TempDir()
	restored, err := m.RestoreBackup(ctx, &backupv1.RestoreBackupRequest{
		BackupId:   info.GetId(),
		TargetPath: target,
	})
	if err != nil {
		t.Fatalf("RestoreBackup: %v", err)
	}
	if restored.GetStatus() != "ok" {
		t.Fatalf("restore resp: %+v", restored)
	}
	if string(peer.data) != "peer-only-state" {
		t.Fatalf("ImportState: got %q want peer-only-state", peer.data)
	}
}

func TestCreateBackupEmptyFails(t *testing.T) {
	m := testModule(t)
	ctx := context.Background()
	_, err := m.CreateBackup(ctx, &backupv1.CreateBackupRequest{})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("want FailedPrecondition, got %v", err)
	}
}

func TestRoundTripCreateListRestoreDelete(t *testing.T) {
	peer := &memPeer{data: []byte("peer-state-v1")}
	m := testModule(t, BackupablePeer{ID: "mod-a", Backend: peer})
	ctx := context.Background()

	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "hello.txt"), []byte("hello world"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "nested", "x.bin"), []byte{1, 2, 3}, 0600); err != nil {
		t.Fatal(err)
	}

	created, err := m.CreateBackup(ctx, &backupv1.CreateBackupRequest{
		SourcePaths: []string{src},
		ModuleIds:   []string{"mod-a"},
	})
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}
	info := created.GetBackup()
	if info.GetId() == "" || info.GetSizeBytes() <= 0 || info.GetChecksumSha256() == "" {
		t.Fatalf("unexpected backup info: %+v", info)
	}
	if len(info.GetModuleIds()) != 1 || info.GetModuleIds()[0] != "mod-a" {
		t.Fatalf("module ids: %v", info.GetModuleIds())
	}

	listed, err := m.ListBackups(ctx, &backupv1.ListBackupsRequest{})
	if err != nil {
		t.Fatalf("ListBackups: %v", err)
	}
	if len(listed.GetBackups()) != 1 || listed.GetBackups()[0].GetId() != info.GetId() {
		t.Fatalf("list: %+v", listed.GetBackups())
	}

	peer.data = []byte("mutated")
	target := t.TempDir()
	restored, err := m.RestoreBackup(ctx, &backupv1.RestoreBackupRequest{
		BackupId:   info.GetId(),
		TargetPath: target,
	})
	if err != nil {
		t.Fatalf("RestoreBackup: %v", err)
	}
	if restored.GetStatus() != "ok" || restored.GetFilesRestored() < 2 {
		t.Fatalf("restore resp: %+v", restored)
	}

	got, err := os.ReadFile(filepath.Join(target, "data", filepath.Base(src), "hello.txt"))
	if err != nil {
		t.Fatalf("read restored hello: %v", err)
	}
	if string(got) != "hello world" {
		t.Fatalf("hello content: %q", got)
	}
	gotNested, err := os.ReadFile(filepath.Join(target, "data", filepath.Base(src), "nested", "x.bin"))
	if err != nil {
		t.Fatalf("read restored nested: %v", err)
	}
	if !bytes.Equal(gotNested, []byte{1, 2, 3}) {
		t.Fatalf("nested content: %v", gotNested)
	}
	if string(peer.data) != "peer-state-v1" {
		t.Fatalf("peer import: %q", peer.data)
	}

	if _, err := m.DeleteBackup(ctx, &backupv1.DeleteBackupRequest{BackupId: info.GetId()}); err != nil {
		t.Fatalf("DeleteBackup: %v", err)
	}
	listed, err = m.ListBackups(ctx, &backupv1.ListBackupsRequest{})
	if err != nil {
		t.Fatalf("ListBackups after delete: %v", err)
	}
	if len(listed.GetBackups()) != 0 {
		t.Fatalf("expected empty list, got %+v", listed.GetBackups())
	}
}

func TestRestoreMissingArchive(t *testing.T) {
	m := testModule(t)
	ctx := context.Background()
	m.mu.Lock()
	m.backups["ghost"] = backupMeta{ID: "ghost", Checksum: "abc"}
	m.mu.Unlock()

	_, err := m.RestoreBackup(ctx, &backupv1.RestoreBackupRequest{
		BackupId:   "ghost",
		TargetPath: t.TempDir(),
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("want NotFound, got %v", err)
	}
}

func TestRestoreRequiresTarget(t *testing.T) {
	m := testModule(t)
	_, err := m.RestoreBackup(context.Background(), &backupv1.RestoreBackupRequest{BackupId: "x"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("want InvalidArgument, got %v", err)
	}
}

func TestRestoreRejectsTraversal(t *testing.T) {
	m := testModule(t)
	ctx := context.Background()

	id := "evil"
	path := archivePath(m.dir, id)
	if err := os.MkdirAll(m.dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeEvilArchive(path); err != nil {
		t.Fatal(err)
	}
	sum, size, err := sha256File(path)
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.backups[id] = backupMeta{ID: id, Size: size, Checksum: sum}
	m.mu.Unlock()

	_, err = m.RestoreBackup(ctx, &backupv1.RestoreBackupRequest{
		BackupId:   id,
		TargetPath: t.TempDir(),
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("want InvalidArgument for traversal, got %v", err)
	}
}

func writeEvilArchive(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	payload := []byte("pwned")
	hdr := &tar.Header{
		Name: "../escape.txt",
		Mode: 0600,
		Size: int64(len(payload)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	if _, err := tw.Write(payload); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gw.Close()
}

func TestSafeJoin(t *testing.T) {
	root := t.TempDir()
	if _, err := safeJoin(root, "../etc/passwd"); err == nil {
		t.Fatal("expected escape failure")
	}
	dest, err := safeJoin(root, "ok/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(dest) != filepath.Join(root, "ok") {
		t.Fatalf("dest=%q", dest)
	}
}
