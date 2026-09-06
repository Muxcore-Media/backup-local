package internal

import (
	"log/slog"
	"os"
	"sort"
	"time"
)

const (
	defaultRetentionCount = 10
	defaultRetentionDays  = 0
)

// retentionConfig holds backup retention policy. Zero count or days disables that rule.
type retentionConfig struct {
	Count int
	Days  int
}

func retentionFromEnv() retentionConfig {
	count := defaultRetentionCount
	days := defaultRetentionDays
	if v := os.Getenv("BACKUP_RETENTION_COUNT"); v != "" {
		count = envInt("BACKUP_RETENTION_COUNT", 0)
	}
	if v := os.Getenv("BACKUP_RETENTION_DAYS"); v != "" {
		days = envInt("BACKUP_RETENTION_DAYS", 0)
	}
	return retentionConfig{Count: count, Days: days}
}

func (c retentionConfig) enabled() bool {
	return c.Count > 0 || c.Days > 0
}

// backupsToDelete returns IDs of backups that exceed retention policy.
// Backups are sorted newest-first. A backup is deleted when it is beyond the
// count limit and/or older than the age limit (either rule may apply).
func backupsToDelete(backups []backupMeta, cfg retentionConfig) []string {
	if !cfg.enabled() || len(backups) == 0 {
		return nil
	}

	sorted := append([]backupMeta(nil), backups...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Timestamp > sorted[j].Timestamp
	})

	var cutoff int64
	if cfg.Days > 0 {
		cutoff = time.Now().AddDate(0, 0, -cfg.Days).Unix()
	}

	seen := make(map[string]struct{})
	var ids []string
	for i, b := range sorted {
		overCount := cfg.Count > 0 && i >= cfg.Count
		overAge := cfg.Days > 0 && b.Timestamp < cutoff
		if overCount || overAge {
			if _, ok := seen[b.ID]; !ok {
				seen[b.ID] = struct{}{}
				ids = append(ids, b.ID)
			}
		}
	}
	return ids
}

// applyRetention deletes backups that exceed the configured retention policy.
func (m *Module) applyRetention() {
	m.mu.Lock()
	cfg := m.retention
	list := make([]backupMeta, 0, len(m.backups))
	for _, b := range m.backups {
		list = append(list, b)
	}
	m.mu.Unlock()

	ids := backupsToDelete(list, cfg)
	if len(ids) == 0 {
		return
	}

	for _, id := range ids {
		if err := m.deleteBackupByID(id); err != nil {
			slog.Warn("retention delete failed", "id", id, "error", err)
		} else {
			slog.Info("retention deleted backup", "id", id)
		}
	}
}

// deleteBackupByID removes a backup from the index and deletes its archive.
func (m *Module) deleteBackupByID(id string) error {
	m.mu.Lock()
	meta, ok := m.backups[id]
	dir := m.dir
	if ok {
		delete(m.backups, id)
		m.saveIndex()
	}
	m.mu.Unlock()
	if !ok {
		return nil
	}
	_ = os.Remove(archivePath(dir, meta.ID))
	return nil
}
