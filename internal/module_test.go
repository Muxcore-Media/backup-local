package internal

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	backupv1 "github.com/Muxcore-Media/backup-local/muxcore/backup/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
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
	t.Setenv("BACKUP_ALLOWED_ROOTS", "")
	t.Setenv("BACKUP_ENCRYPT_KEY", "")
	dir := t.TempDir()
	m := NewModule(Config{
		Dir:      dir,
		GRPCAddr: "127.0.0.1:0",
		HTTPAddr: "127.0.0.1:0",
		Peers:    peers,
	})
	return m
}

func testAllowedRoot(t *testing.T, m *Module, root string) {
	t.Helper()
	m.mu.Lock()
	m.allowedRoots = parseAllowedRoots(append(m.sources, root), "")
	m.mu.Unlock()
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
	if info.HTTPAddr != m.httpAddr {
		t.Fatalf("HTTPAddr=%q want %q", info.HTTPAddr, m.httpAddr)
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
	if err := m.Health(ctx); err != nil {
		t.Fatalf("Health: %v", err)
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

	target := t.TempDir()
	testAllowedRoot(t, m, target)
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
	testAllowedRoot(t, m, src)
	if err := os.WriteFile(filepath.Join(src, "hello.txt"), []byte("hello world"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "nested", "x.bin"), []byte{1, 2, 3}, 0600); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.sources = []string{src}
	m.mu.Unlock()

	created, err := m.CreateBackup(ctx, &backupv1.CreateBackupRequest{
		ModuleIds: []string{"mod-a"},
	})
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}
	info := created.GetBackup()

	listed, err := m.ListBackups(ctx, &backupv1.ListBackupsRequest{})
	if err != nil {
		t.Fatalf("ListBackups: %v", err)
	}
	if len(listed.GetBackups()) != 1 {
		t.Fatalf("list: %+v", listed.GetBackups())
	}

	peer.data = []byte("mutated")
	target := t.TempDir()
	testAllowedRoot(t, m, target)
	restored, err := m.RestoreBackup(ctx, &backupv1.RestoreBackupRequest{
		BackupId:   info.GetId(),
		TargetPath: target,
	})
	if err != nil {
		t.Fatalf("RestoreBackup: %v", err)
	}
	if restored.GetFilesRestored() < 2 {
		t.Fatalf("restore resp: %+v", restored)
	}

	got, err := os.ReadFile(filepath.Join(target, "data", filepath.Base(src), "hello.txt"))
	if err != nil {
		t.Fatalf("read restored hello: %v", err)
	}
	if string(got) != "hello world" {
		t.Fatalf("hello content: %q", got)
	}
	if string(peer.data) != "peer-state-v1" {
		t.Fatalf("peer import: %q", peer.data)
	}

	if _, err := m.DeleteBackup(ctx, &backupv1.DeleteBackupRequest{BackupId: info.GetId()}); err != nil {
		t.Fatalf("DeleteBackup: %v", err)
	}
}

func TestRestoreMissingArchive(t *testing.T) {
	m := testModule(t)
	ctx := context.Background()
	target := t.TempDir()
	testAllowedRoot(t, m, target)
	m.mu.Lock()
	m.backups["ghost"] = backupMeta{ID: "ghost", Checksum: "abc"}
	m.mu.Unlock()

	_, err := m.RestoreBackup(ctx, &backupv1.RestoreBackupRequest{
		BackupId:   "ghost",
		TargetPath: target,
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
	target := t.TempDir()
	testAllowedRoot(t, m, target)

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
		TargetPath: target,
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("want InvalidArgument for traversal, got %v", err)
	}
}

func TestRestoreChecksumMismatch(t *testing.T) {
	m := testModule(t)
	ctx := context.Background()
	peer := &memPeer{data: []byte("state")}
	m.peers["p"] = peer
	target := t.TempDir()
	testAllowedRoot(t, m, target)

	created, err := m.CreateBackup(ctx, &backupv1.CreateBackupRequest{ModuleIds: []string{"p"}})
	if err != nil {
		t.Fatal(err)
	}
	id := created.GetBackup().GetId()
	m.mu.Lock()
	meta := m.backups[id]
	meta.Checksum = "deadbeef"
	m.backups[id] = meta
	m.mu.Unlock()

	_, err = m.RestoreBackup(ctx, &backupv1.RestoreBackupRequest{
		BackupId:   id,
		TargetPath: target,
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("want FailedPrecondition, got %v", err)
	}
}

func TestIndexPersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	peer := &memPeer{data: []byte("persist")}
	m1 := NewModule(Config{Dir: dir, GRPCAddr: "127.0.0.1:0", HTTPAddr: "127.0.0.1:0", Peers: []BackupablePeer{{ID: "p", Backend: peer}}})
	ctx := context.Background()
	if err := m1.Init(ctx); err != nil {
		t.Fatal(err)
	}
	created, err := m1.CreateBackup(ctx, &backupv1.CreateBackupRequest{ModuleIds: []string{"p"}})
	if err != nil {
		t.Fatal(err)
	}
	id := created.GetBackup().GetId()

	m2 := NewModule(Config{Dir: dir, GRPCAddr: "127.0.0.1:0", HTTPAddr: "127.0.0.1:0"})
	if err := m2.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if len(m2.backups) != 1 {
		t.Fatalf("expected 1 backup after reload, got %d", len(m2.backups))
	}
	if _, err := os.Stat(archivePath(dir, id)); err != nil {
		t.Fatalf("archive missing: %v", err)
	}
}

func TestReconcileDropsMissingArchive(t *testing.T) {
	dir := t.TempDir()
	m := NewModule(Config{Dir: dir, GRPCAddr: "127.0.0.1:0", HTTPAddr: "127.0.0.1:0"})
	ctx := context.Background()
	if err := m.Init(ctx); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.backups["orphan"] = backupMeta{ID: "orphan", Timestamp: time.Now().Unix()}
	m.mu.Unlock()
	if err := m.saveIndex(); err != nil {
		t.Fatal(err)
	}
	m2 := NewModule(Config{Dir: dir, GRPCAddr: "127.0.0.1:0", HTTPAddr: "127.0.0.1:0"})
	if err := m2.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := m2.backups["orphan"]; ok {
		t.Fatal("orphan index entry should be removed when archive missing")
	}
}

func TestExcludeGlobsSkipIncomplete(t *testing.T) {
	m := testModule(t)
	src := t.TempDir()
	testAllowedRoot(t, m, src)
	m.mu.Lock()
	m.sources = []string{src}
	m.mu.Unlock()
	if err := os.WriteFile(filepath.Join(src, "keep.txt"), []byte("yes"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "download.part"), []byte("no"), 0600); err != nil {
		t.Fatal(err)
	}

	created, err := m.CreateBackup(context.Background(), &backupv1.CreateBackupRequest{})
	if err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	testAllowedRoot(t, m, target)
	if _, err := m.RestoreBackup(context.Background(), &backupv1.RestoreBackupRequest{
		BackupId: created.GetBackup().GetId(), TargetPath: target,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target, "data", filepath.Base(src), "keep.txt")); err != nil {
		t.Fatal("keep.txt missing")
	}
	if _, err := os.Stat(filepath.Join(target, "data", filepath.Base(src), "download.part")); !os.IsNotExist(err) {
		t.Fatal("download.part should be excluded")
	}
}

func TestSourcePathAllowlist(t *testing.T) {
	m := testModule(t)
	allowed := t.TempDir()
	testAllowedRoot(t, m, allowed)
	outside := t.TempDir()
	_, err := m.CreateBackup(context.Background(), &backupv1.CreateBackupRequest{
		SourcePaths: []string{outside},
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("want InvalidArgument for outside source, got %v", err)
	}
}

func TestPruneByCount(t *testing.T) {
	m := testModule(t, BackupablePeer{ID: "p", Backend: &memPeer{data: []byte("x")}})
	m.setRetention(2, 0)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := m.CreateBackup(ctx, &backupv1.CreateBackupRequest{ModuleIds: []string{"p"}}); err != nil {
			t.Fatal(err)
		}
	}
	list, err := m.ListBackups(ctx, &backupv1.ListBackupsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.GetBackups()) != 2 {
		t.Fatalf("want 2 backups after prune, got %d", len(list.GetBackups()))
	}
}

func TestPruneByAge(t *testing.T) {
	m := testModule(t, BackupablePeer{ID: "p", Backend: &memPeer{data: []byte("x")}})
	m.setRetention(0, 7)
	ctx := context.Background()

	keep, err := m.CreateBackup(ctx, &backupv1.CreateBackupRequest{ModuleIds: []string{"p"}})
	if err != nil {
		t.Fatal(err)
	}
	stale, err := m.CreateBackup(ctx, &backupv1.CreateBackupRequest{ModuleIds: []string{"p"}})
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	old := m.backups[stale.GetBackup().GetId()]
	old.Timestamp = time.Now().Add(-10 * 24 * time.Hour).Unix()
	m.backups[stale.GetBackup().GetId()] = old
	m.mu.Unlock()

	if err := m.pruneBackups(ctx); err != nil {
		t.Fatal(err)
	}
	list, err := m.ListBackups(ctx, &backupv1.ListBackupsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.GetBackups()) != 1 {
		t.Fatalf("want 1 backup after age prune, got %d", len(list.GetBackups()))
	}
	if list.GetBackups()[0].GetId() != keep.GetBackup().GetId() {
		t.Fatalf("kept %q want %q", list.GetBackups()[0].GetId(), keep.GetBackup().GetId())
	}
}

func TestHealthOKAndDegraded(t *testing.T) {
	m := testModule(t)
	ctx := context.Background()
	if err := m.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.Health(ctx); err != nil {
		t.Fatalf("Health: %v", err)
	}

	bad := filepath.Join(m.dir, "index.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.Health(ctx); err == nil {
		t.Fatal("expected degraded health for corrupt index")
	}
}

func TestEncryptedBackupRoundTrip(t *testing.T) {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	t.Setenv("BACKUP_ENCRYPT_KEY", hex.EncodeToString(key))

	dir := t.TempDir()
	m := NewModule(Config{
		Dir:      dir,
		GRPCAddr: "127.0.0.1:0",
		HTTPAddr: "127.0.0.1:0",
		Peers:    []BackupablePeer{{ID: "p", Backend: &memPeer{data: []byte("secret")}}},
	})
	ctx := context.Background()
	if err := m.Init(ctx); err != nil {
		t.Fatal(err)
	}
	created, err := m.CreateBackup(ctx, &backupv1.CreateBackupRequest{ModuleIds: []string{"p"}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(archivePath(dir, created.GetBackup().GetId()))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.HasPrefix(raw, []byte{0x1f, 0x8b}) {
		t.Fatal("expected encrypted blob, got plaintext gzip")
	}
	target := t.TempDir()
	testAllowedRoot(t, m, target)
	if _, err := m.RestoreBackup(ctx, &backupv1.RestoreBackupRequest{
		BackupId: created.GetBackup().GetId(), TargetPath: target,
	}); err != nil {
		t.Fatal(err)
	}
}

type bufPeerServer struct {
	backupv1.UnimplementedBackupableServiceServer
	export []byte
}

func (s *bufPeerServer) ExportState(ctx context.Context, _ *backupv1.ExportStateRequest) (*backupv1.ExportStateResponse, error) {
	return &backupv1.ExportStateResponse{Data: append([]byte(nil), s.export...)}, nil
}

func (s *bufPeerServer) ImportState(ctx context.Context, req *backupv1.ImportStateRequest) (*backupv1.ImportStateResponse, error) {
	s.export = append([]byte(nil), req.GetData()...)
	return &backupv1.ImportStateResponse{Status: "ok"}, nil
}

func TestGRPCBackupablePeerViaBufconn(t *testing.T) {
	const bufSize = 1 << 20
	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer()
	peerSrv := &bufPeerServer{export: []byte("grpc-peer-state")}
	backupv1.RegisterBackupableServiceServer(srv, peerSrv)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop() })

	dialer := func(context.Context, string) (net.Conn, error) {
		return lis.Dial()
	}
	conn, err := grpc.NewClient("passthrough:///bufconn",
		grpc.WithContextDialer(dialer),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	g := &grpcBackupable{id: "remote-db", client: backupv1.NewBackupableServiceClient(conn), conn: conn}
	m := testModule(t, BackupablePeer{ID: "remote-db", Backend: g})
	ctx := context.Background()
	created, err := m.CreateBackup(ctx, &backupv1.CreateBackupRequest{ModuleIds: []string{"remote-db"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(created.GetBackup().GetModuleIds()) != 1 {
		t.Fatalf("module ids: %v", created.GetBackup().GetModuleIds())
	}
	peerSrv.export = []byte("wiped")
	target := t.TempDir()
	testAllowedRoot(t, m, target)
	if _, err := m.RestoreBackup(ctx, &backupv1.RestoreBackupRequest{
		BackupId: created.GetBackup().GetId(), TargetPath: target,
	}); err != nil {
		t.Fatal(err)
	}
	if string(peerSrv.export) != "grpc-peer-state" {
		t.Fatalf("import via grpc: %q", peerSrv.export)
	}
}

func TestSafeJoin(t *testing.T) {
	root := t.TempDir()
	if _, err := safeJoin(root, "../etc/passwd"); err == nil {
		t.Fatal("expected escape failure")
	}
}

func TestSettingsBackupDirAndSources(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "f.txt"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	m := NewModule(Config{Dir: dirA, GRPCAddr: "127.0.0.1:0", HTTPAddr: "127.0.0.1:0"})
	m.mu.Lock()
	m.allowedRoots = parseAllowedRoots([]string{src}, "")
	m.mu.Unlock()
	ctx := context.Background()
	if err := m.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.UpdateSetting("source_dirs", src); err != nil {
		t.Fatal(err)
	}
	if err := m.UpdateSetting("backup_dir", dirB); err != nil {
		t.Fatal(err)
	}
	resp, err := m.CreateBackup(ctx, &backupv1.CreateBackupRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dirB, resp.GetBackup().GetId()+".tar.gz")); err != nil {
		t.Fatalf("archive missing in new dir: %v", err)
	}
}

func TestDeleteBackupFailsWhenArchiveMissing(t *testing.T) {
	m := testModule(t)
	ctx := context.Background()
	m.mu.Lock()
	m.backups["x"] = backupMeta{ID: "x", Timestamp: time.Now().Unix()}
	m.mu.Unlock()
	if err := m.saveIndex(); err != nil {
		t.Fatal(err)
	}
	_, err := m.DeleteBackup(ctx, &backupv1.DeleteBackupRequest{BackupId: "x"})
	if err == nil {
		t.Fatal("expected error when archive file missing")
	}
}

func TestWriteEvilArchiveHelper(t *testing.T) {
	path := filepath.Join(t.TempDir(), "evil.tar.gz")
	if err := writeEvilArchive(path); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	gr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gr.Close() }()
	tr := tar.NewReader(gr)
	hdr, err := tr.Next()
	if err != nil {
		t.Fatal(err)
	}
	if hdr.Name != "../escape.txt" {
		t.Fatalf("hdr=%q", hdr.Name)
	}
}
