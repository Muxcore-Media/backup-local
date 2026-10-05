package integsupport_test

import (
	"context"
	"testing"

	backupv1 "github.com/Muxcore-Media/backup-local/muxcore/backup/v1"

	"github.com/Muxcore-Media/backup-local/integsupport"
)

func TestNewTestModuleBackupVerifyList(t *testing.T) {
	ctx := context.Background()
	m := integsupport.NewTestModule(t, integsupport.Config{})
	if err := integsupport.Start(ctx, m); err != nil {
		t.Fatalf("start: %v", err)
	}

	list, err := m.ListBackups(ctx, &backupv1.ListBackupsRequest{})
	if err != nil || len(list.GetBackups()) != 0 {
		t.Fatalf("empty list: %v %v", list, err)
	}

	created, err := m.CreateBackup(ctx, &backupv1.CreateBackupRequest{ModuleIds: []string{integsupport.DefaultPeerID}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := created.GetBackup().GetId()
	if id == "" {
		t.Fatal("empty backup id")
	}

	list, err = m.ListBackups(ctx, &backupv1.ListBackupsRequest{})
	if err != nil || len(list.GetBackups()) != 1 || list.GetBackups()[0].GetId() != id {
		t.Fatalf("list after create: %v %v", list, err)
	}

	v, err := m.VerifyBackup(ctx, &backupv1.VerifyBackupRequest{BackupId: id, RestoreTest: true})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !v.GetValid() || !v.GetChecksumOk() || !v.GetRestoreTestOk() || v.GetFilesInArchive() != 1 {
		t.Fatalf("unexpected verify response: %v", v)
	}

	if _, err := m.VerifyBackup(ctx, &backupv1.VerifyBackupRequest{BackupId: "nope"}); err == nil {
		t.Fatal("expected NotFound for unknown backup")
	}
}
