package internal

import (
	"context"
	"testing"
	"time"

	backupv1 "github.com/Muxcore-Media/backup-local/muxcore/backup/v1"
)

func TestBackupsToDeleteByCount(t *testing.T) {
	backups := make([]backupMeta, 5)
	for i := range backups {
		backups[i] = backupMeta{
			ID:        "b" + string(rune('a'+i)),
			Timestamp: int64(1000 - i),
		}
	}
	cfg := retentionConfig{Count: 2, Days: 0}
	ids := backupsToDelete(backups, cfg)
	if len(ids) != 3 {
		t.Fatalf("expected 3 deletions, got %v", ids)
	}
}

func TestBackupsToDeleteByAge(t *testing.T) {
	now := time.Now().Unix()
	backups := []backupMeta{
		{ID: "new", Timestamp: now},
		{ID: "old", Timestamp: now - 40*24*3600},
	}
	cfg := retentionConfig{Count: 0, Days: 30}
	ids := backupsToDelete(backups, cfg)
	if len(ids) != 1 || ids[0] != "old" {
		t.Fatalf("expected [old], got %v", ids)
	}
}

func TestBackupsToDeleteDisabled(t *testing.T) {
	backups := []backupMeta{{ID: "a", Timestamp: 1}}
	cfg := retentionConfig{Count: 0, Days: 0}
	if ids := backupsToDelete(backups, cfg); len(ids) != 0 {
		t.Fatalf("expected no deletions, got %v", ids)
	}
}

func TestApplyRetentionCount(t *testing.T) {
	m := testModule(t, BackupablePeer{ID: "p", Backend: &memPeer{data: []byte("x")}})
	m.retention = retentionConfig{Count: 2, Days: 0}
	ctx := context.Background()

	for i := 0; i < 4; i++ {
		_, err := m.CreateBackup(ctx, &backupv1.CreateBackupRequest{ModuleIds: []string{"p"}})
		if err != nil {
			t.Fatalf("CreateBackup %d: %v", i, err)
		}
	}

	listed, err := m.ListBackups(ctx, &backupv1.ListBackupsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.GetBackups()) != 2 {
		t.Fatalf("expected 2 backups after retention, got %d", len(listed.GetBackups()))
	}
}

func TestValidateScheduleCron(t *testing.T) {
	if err := validateScheduleCron("0 3 * * *"); err != nil {
		t.Fatalf("valid cron rejected: %v", err)
	}
	if err := validateScheduleCron(""); err == nil {
		t.Fatal("empty cron should fail")
	}
	if err := validateScheduleCron("not a cron"); err == nil {
		t.Fatal("invalid cron should fail")
	}
}

func TestUpdateSettingScheduleCron(t *testing.T) {
	m := testModule(t)
	if err := m.UpdateSetting("backup_schedule_cron", "0 2 * * *"); err != nil {
		t.Fatal(err)
	}
	if m.schedule != "0 2 * * *" {
		t.Fatalf("schedule=%q", m.schedule)
	}
	if err := m.UpdateSetting("backup_schedule_cron", "invalid"); err == nil {
		t.Fatal("invalid cron should fail update")
	}
}

func TestUpdateSettingRetention(t *testing.T) {
	m := testModule(t)
	if err := m.UpdateSetting("backup_retention_count", "5"); err != nil {
		t.Fatal(err)
	}
	if m.retention.Count != 5 {
		t.Fatalf("count=%d", m.retention.Count)
	}
	if err := m.UpdateSetting("backup_retention_days", "14"); err != nil {
		t.Fatal(err)
	}
	if m.retention.Days != 14 {
		t.Fatalf("days=%d", m.retention.Days)
	}
}

func TestSettingsIncludeScheduleAndRetention(t *testing.T) {
	m := testModule(t)
	defs := m.Settings()
	if len(defs) != 5 {
		t.Fatalf("expected 5 settings, got %d: %+v", len(defs), defs)
	}
}
