package internal

import (
	"log/slog"
	"time"
)

// logBackupOp emits structured slog for backup operations with id, action, duration, and size.
func logBackupOp(action, backupID string, start time.Time, size int64, attrs ...any) {
	args := make([]any, 0, 8+len(attrs))
	args = append(args, "action", action, "backup_id", backupID, "duration_ms", time.Since(start).Milliseconds())
	if size >= 0 {
		args = append(args, "size_bytes", size)
	}
	args = append(args, attrs...)
	slog.Info("backup-local", args...)
}

func logBackupWarn(action, backupID string, start time.Time, err error, attrs ...any) {
	args := make([]any, 0, 8+len(attrs))
	args = append(args, "action", action, "backup_id", backupID, "duration_ms", time.Since(start).Milliseconds(), "error", err)
	args = append(args, attrs...)
	slog.Warn("backup-local", args...)
}
