package internal

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"

	backupv1 "github.com/Muxcore-Media/backup-local/muxcore/backup/v1"
	"github.com/robfig/cron/v3"
)

// validateScheduleCron returns an error when expr is empty or not a valid cron expression.
func validateScheduleCron(expr string) error {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return fmt.Errorf("schedule cron expression must not be empty")
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	if _, err := parser.Parse(expr); err != nil {
		return fmt.Errorf("invalid cron expression %q: %w", expr, err)
	}
	return nil
}

func scheduleFromEnv() string {
	return strings.TrimSpace(os.Getenv("BACKUP_SCHEDULE_CRON"))
}

func envInt(key string, defaultVal int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		slog.Warn("invalid integer env, using default", "key", key, "value", v, "default", defaultVal)
		return defaultVal
	}
	return n
}

type backupScheduler struct {
	mu     sync.Mutex
	cron   *cron.Cron
	expr   string
	cancel context.CancelFunc
}

func newBackupScheduler() *backupScheduler {
	return &backupScheduler{}
}

func (s *backupScheduler) start(ctx context.Context, m *Module, expr string) error {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil
	}
	if err := validateScheduleCron(expr); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	if s.cron != nil {
		s.cron.Stop()
		s.cron = nil
	}

	runCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.expr = expr

	c := cron.New(cron.WithParser(cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)))
	_, err := c.AddFunc(expr, func() {
		m.runScheduledBackup(runCtx)
	})
	if err != nil {
		cancel()
		return fmt.Errorf("register cron job: %w", err)
	}
	s.cron = c
	c.Start()
	slog.Info("backup schedule started", "cron", expr)
	return nil
}

func (s *backupScheduler) stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	if s.cron != nil {
		ctx := s.cron.Stop()
		<-ctx.Done()
		s.cron = nil
	}
}

func (m *Module) runScheduledBackup(ctx context.Context) {
	slog.Info("scheduled backup starting")
	resp, err := m.CreateBackup(ctx, &backupv1.CreateBackupRequest{})
	if err != nil {
		slog.Error("scheduled backup failed", "error", err)
		return
	}
	slog.Info("scheduled backup completed", "id", resp.GetBackup().GetId())
}
