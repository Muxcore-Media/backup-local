package internal

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"time"

	backupv1 "github.com/Muxcore-Media/backup-local/muxcore/backup/v1"
	"github.com/robfig/cron/v3"
)

func (m *Module) startScheduleLoop(ctx context.Context) {
	expr := m.scheduleCron()
	if expr == "" {
		return
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	sched, err := parser.Parse(expr)
	if err != nil {
		slog.Warn("backup-local: invalid schedule", "cron", expr, "error", err)
		return
	}
	go func() {
		for {
			next := sched.Next(time.Now())
			wait := time.Until(next)
			if wait < 0 {
				wait = time.Second
			}
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
				if _, err := m.CreateBackup(ctx, nil); err != nil {
					slog.Warn("backup-local: scheduled backup failed", "error", err)
				}
			}
		}
	}()
	slog.Info("backup-local: scheduled backups enabled", "cron", expr)
}

func (m *Module) scheduleCron() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.scheduleCronVal != "" {
		return m.scheduleCronVal
	}
	return os.Getenv("BACKUP_SCHEDULE")
}

func (m *Module) pruneBackups(ctx context.Context) error {
	m.mu.Lock()
	maxBackups := m.maxBackups
	maxAgeDays := m.maxAgeDays
	m.mu.Unlock()

	if maxBackups <= 0 && maxAgeDays <= 0 {
		return nil
	}

	list := make([]backupMeta, 0, len(m.backups))
	m.mu.Lock()
	for _, b := range m.backups {
		list = append(list, b)
	}
	m.mu.Unlock()

	sort.Slice(list, func(i, j int) bool {
		return list[i].Timestamp > list[j].Timestamp
	})

	cutoff := int64(0)
	if maxAgeDays > 0 {
		cutoff = time.Now().Add(-time.Duration(maxAgeDays) * 24 * time.Hour).Unix()
	}

	var toDelete []string
	for i, b := range list {
		if maxBackups > 0 && i >= maxBackups {
			toDelete = append(toDelete, b.ID)
			continue
		}
		if cutoff > 0 && b.Timestamp < cutoff {
			toDelete = append(toDelete, b.ID)
		}
	}

	for _, id := range toDelete {
		if _, err := m.DeleteBackup(ctx, &backupv1.DeleteBackupRequest{BackupId: id}); err != nil {
			slog.Warn("backup-local: prune delete failed", "id", id, "error", err)
		}
	}
	return nil
}

func (m *Module) setRetention(maxBackups, maxAgeDays int) {
	m.mu.Lock()
	m.maxBackups = maxBackups
	m.maxAgeDays = maxAgeDays
	m.mu.Unlock()
}

func parseRetentionInt(key, val string, current int) (int, error) {
	val = stringsTrim(val)
	if val == "" {
		return current, nil
	}
	n, err := strconvAtoi(val)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer", key)
	}
	return n, nil
}

// small helpers to avoid importing strconv in module.go settings path duplication
func stringsTrim(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}

func strconvAtoi(s string) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("invalid integer")
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}
