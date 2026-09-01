package internal

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/Muxcore-Media/core/pkg/contracts"
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
	}
}

func (m *Module) updateSetting(key, value string) error {
	switch key {
	case "backup_dir", "BACKUP_DIR":
		value = strings.TrimSpace(value)
		if value == "" {
			return fmt.Errorf("backup_dir must not be empty")
		}
		if err := os.MkdirAll(value, 0700); err != nil {
			return fmt.Errorf("create backup dir: %w", err)
		}
		m.mu.Lock()
		m.dir = value
		m.backups = make(map[string]backupMeta)
		err := m.loadIndex()
		m.mu.Unlock()
		return err
	case "source_dirs", "BACKUP_SOURCE_DIRS":
		m.mu.Lock()
		m.sources = splitCSV(value)
		m.allowedRoots = parseAllowedRoots(m.sources, os.Getenv("BACKUP_ALLOWED_ROOTS"))
		m.mu.Unlock()
		return nil
	case "exclude_globs", "BACKUP_EXCLUDE":
		m.mu.Lock()
		m.excludeGlobs = parseExcludeGlobs("", value)
		m.mu.Unlock()
		return nil
	case "max_backups":
		n, err := parseRetentionInt("max_backups", value, m.maxBackups)
		if err != nil {
			return err
		}
		m.setRetention(n, m.maxAgeDays)
		return nil
	case "max_age_days":
		n, err := parseRetentionInt("max_age_days", value, m.maxAgeDays)
		if err != nil {
			return err
		}
		m.setRetention(m.maxBackups, n)
		return nil
	case "schedule_cron", "BACKUP_SCHEDULE":
		m.mu.Lock()
		m.scheduleCronVal = strings.TrimSpace(value)
		m.mu.Unlock()
		return nil
	default:
		return fmt.Errorf("unknown setting %q", key)
	}
}
