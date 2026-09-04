package internal

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Muxcore-Media/core/pkg/contracts"
	"github.com/robfig/cron/v3"
)

func (m *Module) Settings() []contracts.SettingDef {
	return m.settingsDefs()
}

func (m *Module) UpdateSetting(key, value string) error {
	return m.updateSetting(key, value)
}

func (m *Module) settingsDefs() []contracts.SettingDef {
	m.mu.Lock()
	dir := m.dir
	sources := strings.Join(m.sources, ",")
	excludes := strings.Join(m.excludeGlobs, ",")
	schedule := m.scheduleCronVal
	maxBackups := m.maxBackups
	maxAge := m.maxAgeDays
	verifyOnCreate := m.verifyOnCreate
	incrementalDefault := m.incrementalDefault
	encryptionEnabled := m.cryptor != nil && m.cryptor.enabled()
	m.mu.Unlock()
	return []contracts.SettingDef{
		{
			Key:         "backup_dir",
			Label:       "Backup Directory",
			Type:        contracts.SettingTypeString,
			Value:       dir,
			Default:     "backups",
			Description: "Directory for archives + index.json (BACKUP_DIR); updates reload the index",
			Group:       "Storage",
		},
		{
			Key:         "source_dirs",
			Label:       "Source Directories",
			Type:        contracts.SettingTypeString,
			Value:       sources,
			Default:     "",
			Description: "Comma-separated dirs archived by CreateBackup (BACKUP_SOURCE_DIRS)",
			Group:       "Sources",
		},
		{
			Key:         "exclude_globs",
			Label:       "Exclude globs",
			Type:        contracts.SettingTypeString,
			Value:       excludes,
			Default:     strings.Join(defaultExcludeGlobs, ","),
			Description: "Comma-separated globs skipped during archive (BACKUP_EXCLUDE)",
			Group:       "Sources",
		},
		{
			Key:         "max_backups",
			Label:       "Max backups",
			Type:        contracts.SettingTypeString,
			Value:       strconv.Itoa(maxBackups),
			Default:     "0",
			Description: "Retain at most N backups (0 = unlimited); oldest removed after CreateBackup",
			Group:       "Retention",
		},
		{
			Key:         "max_age_days",
			Label:       "Max age (days)",
			Type:        contracts.SettingTypeString,
			Value:       strconv.Itoa(maxAge),
			Default:     "0",
			Description: "Delete backups older than N days (0 = unlimited)",
			Group:       "Retention",
		},
		{
			Key:         "schedule_cron",
			Label:       "Schedule (cron)",
			Type:        contracts.SettingTypeString,
			Value:       schedule,
			Default:     "",
			Description: "5-field cron for automatic CreateBackup (BACKUP_SCHEDULE); empty disables",
			Group:       "Schedule",
		},
		{
			Key:         "verify_on_create",
			Label:       "Verify on create",
			Type:        contracts.SettingTypeString,
			Value:       boolSetting(verifyOnCreate),
			Default:     "false",
			Description: "Run checksum + restore test after each CreateBackup (BACKUP_VERIFY_ON_CREATE)",
			Group:       "Verification",
		},
		{
			Key:         "incremental_default",
			Label:       "Incremental by default",
			Type:        contracts.SettingTypeString,
			Value:       boolSetting(incrementalDefault),
			Default:     "false",
			Description: "Only archive files changed since the last backup (BACKUP_INCREMENTAL_DEFAULT)",
			Group:       "Sources",
		},
		{
			Key:         "encryption_enabled",
			Label:       "Encryption enabled",
			Type:        contracts.SettingTypeString,
			Value:       boolSetting(encryptionEnabled),
			Default:     "false",
			Description: "Read-only: AES-256-GCM when BACKUP_ENCRYPT_KEY or mesh encryption module is configured",
			Group:       "Storage",
		},
	}
}

func (m *Module) updateSetting(key, value string) error {
	switch key {
	case "backup_dir", "BACKUP_DIR":
		dir, err := validateBackupDir(value)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("create backup dir: %w", err)
		}
		if err := ensureDirPerm(dir, 0700); err != nil {
			return fmt.Errorf("backup dir permissions: %w", err)
		}
		if err := dirWritable(dir); err != nil {
			return fmt.Errorf("backup dir not writable: %w", err)
		}
		m.mu.Lock()
		m.dir = dir
		m.backups = make(map[string]backupMeta)
		err = m.loadIndex()
		m.mu.Unlock()
		if err != nil {
			slog.Warn("backup-local: reload index after backup_dir change", "dir", dir, "error", err)
		}
		slog.Info("backup-local: backup_dir updated", "dir", dir)
		return nil
	case "source_dirs", "BACKUP_SOURCE_DIRS":
		sources := splitCSV(value)
		m.mu.Lock()
		allowed := parseAllowedRoots(sources, os.Getenv("BACKUP_ALLOWED_ROOTS"))
		m.mu.Unlock()
		if err := validateSourceDirs(sources, allowed); err != nil {
			return err
		}
		m.mu.Lock()
		m.sources = sources
		m.allowedRoots = allowed
		m.mu.Unlock()
		slog.Info("backup-local: source_dirs updated", "count", len(sources))
		return nil
	case "exclude_globs", "BACKUP_EXCLUDE":
		globs := parseExcludeGlobs("", value)
		if err := validateExcludeGlobs(globs); err != nil {
			return err
		}
		m.mu.Lock()
		m.excludeGlobs = globs
		m.mu.Unlock()
		slog.Info("backup-local: exclude_globs updated", "count", len(globs))
		return nil
	case "max_backups":
		n, err := parseRetentionInt("max_backups", value, m.maxBackups)
		if err != nil {
			return err
		}
		m.setRetention(n, m.maxAgeDays)
		slog.Info("backup-local: max_backups updated", "value", n)
		return nil
	case "max_age_days":
		n, err := parseRetentionInt("max_age_days", value, m.maxAgeDays)
		if err != nil {
			return err
		}
		m.setRetention(m.maxBackups, n)
		slog.Info("backup-local: max_age_days updated", "value", n)
		return nil
	case "schedule_cron", "BACKUP_SCHEDULE":
		expr := strings.TrimSpace(value)
		if expr != "" {
			parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
			if _, err := parser.Parse(expr); err != nil {
				return fmt.Errorf("schedule_cron: %w", err)
			}
		}
		m.mu.Lock()
		m.scheduleCronVal = expr
		m.mu.Unlock()
		slog.Info("backup-local: schedule_cron updated", "cron", expr)
		return nil
	case "verify_on_create", "BACKUP_VERIFY_ON_CREATE":
		enabled, err := parseBoolSetting("verify_on_create", value)
		if err != nil {
			return err
		}
		m.mu.Lock()
		m.verifyOnCreate = enabled
		m.mu.Unlock()
		slog.Info("backup-local: verify_on_create updated", "value", enabled)
		return nil
	case "incremental_default", "BACKUP_INCREMENTAL_DEFAULT":
		enabled, err := parseBoolSetting("incremental_default", value)
		if err != nil {
			return err
		}
		m.mu.Lock()
		m.incrementalDefault = enabled
		m.mu.Unlock()
		slog.Info("backup-local: incremental_default updated", "value", enabled)
		return nil
	default:
		return fmt.Errorf("unknown setting %q", key)
	}
}

func validateExcludeGlobs(globs []string) error {
	for _, g := range globs {
		g = strings.TrimSpace(g)
		if g == "" {
			continue
		}
		if filepath.IsAbs(g) {
			return fmt.Errorf("exclude_globs: absolute paths are not allowed (%q)", g)
		}
		if strings.Contains(g, "..") {
			return fmt.Errorf("exclude_globs: path traversal is not allowed (%q)", g)
		}
	}
	return nil
}

func boolSetting(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func parseBoolSetting(key, val string) (bool, error) {
	val = strings.TrimSpace(strings.ToLower(val))
	switch val {
	case "", "false", "0", "no", "off":
		return false, nil
	case "true", "1", "yes", "on":
		return true, nil
	default:
		return false, fmt.Errorf("%s must be true or false", key)
	}
}
