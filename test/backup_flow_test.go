package test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Muxcore-Media/backup-local/internal"
	backupv1 "github.com/Muxcore-Media/backup-local/muxcore/backup/v1"
)

type memPeer struct {
	data []byte
}

func (p *memPeer) ExportState(ctx context.Context) ([]byte, error) {
	return append([]byte(nil), p.data...), nil
}

func (p *memPeer) ImportState(ctx context.Context, data []byte) error {
	p.data = append([]byte(nil), data...)
	return nil
}

func TestInProcessBackupFlow(t *testing.T) {
	dir := t.TempDir()
	src := t.TempDir()
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "note.txt"), []byte("flow"), 0600); err != nil {
		t.Fatal(err)
	}

	peer := &memPeer{data: []byte("mesh-state")}
	m := internal.NewModule(internal.Config{
		Dir:          dir,
		SourceDirs:   []string{src},
		AllowedRoots: []string{src, target},
		Peers:        []internal.BackupablePeer{{ID: "fixture", Backend: peer}},
		GRPCAddr:     "127.0.0.1:0",
		HTTPAddr:     "127.0.0.1:0",
	})
	ctx := context.Background()
	if err := m.Init(ctx); err != nil {
		t.Fatal(err)
	}

	created, err := m.CreateBackup(ctx, &backupv1.CreateBackupRequest{ModuleIds: []string{"fixture"}})
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}
	id := created.GetBackup().GetId()
	if id == "" {
		t.Fatal("empty backup id")
	}

	listed, err := m.ListBackups(ctx, &backupv1.ListBackupsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.GetBackups()) != 1 {
		t.Fatalf("list len=%d", len(listed.GetBackups()))
	}

	peer.data = []byte("cleared")
	restored, err := m.RestoreBackup(ctx, &backupv1.RestoreBackupRequest{
		BackupId: id, TargetPath: target,
	})
	if err != nil {
		t.Fatalf("RestoreBackup: %v", err)
	}
	if restored.GetFilesRestored() < 1 {
		t.Fatalf("files restored: %d", restored.GetFilesRestored())
	}
	if string(peer.data) != "mesh-state" {
		t.Fatalf("peer state: %q", peer.data)
	}

	if _, err := m.DeleteBackup(ctx, &backupv1.DeleteBackupRequest{BackupId: id}); err != nil {
		t.Fatal(err)
	}
}
