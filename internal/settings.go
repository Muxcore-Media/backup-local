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
	schedule := m.schedule
	retentionCount := m.retention.Count
	retentionDays := m.retention.Days
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
			Key:         "backup_schedule_cron",
			Label:       "Backup Schedule (cron)",
			Type:        contracts.SettingTypeString,
			Value:       schedule,
			Default:     "",
			Description: "Cron expression for automatic backups (BACKUP_SCHEDULE_CRON); empty disables scheduling",
			Group:       "Schedule",
		},
		{
			Key:         "backup_retention_count",
			Label:       "Retention Count",
			Type:        contracts.SettingTypeString,
			Value:       strconv.Itoa(retentionCount),
			Default:     strconv.Itoa(defaultRetentionCount),
			Description: "Keep at most this many backups; 0 disables count-based retention (BACKUP_RETENTION_COUNT)",
			Group:       "Retention",
		},
		{
			Key:         "backup_retention_days",
			Label:       "Retention Days",
			Type:        contracts.SettingTypeString,
			Value:       strconv.Itoa(retentionDays),
			Default:     strconv.Itoa(defaultRetentionDays),
			Description: "Delete backups older than this many days; 0 disables age-based retention (BACKUP_RETENTION_DAYS)",
			Group:       "Retention",
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
		m.mu.Unlock()
		return nil
	case "backup_schedule_cron", "BACKUP_SCHEDULE_CRON":
		value = strings.TrimSpace(value)
		if value != "" {
			if err := validateScheduleCron(value); err != nil {
				return err
			}
		}
		m.mu.Lock()
		m.schedule = value
		runCtx := m.runCtx
		m.mu.Unlock()
		if runCtx != nil {
			if err := m.scheduler.start(runCtx, m, value); err != nil {
				return fmt.Errorf("restart schedule: %w", err)
			}
		}
		return nil
	case "backup_retention_count", "BACKUP_RETENTION_COUNT":
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || n < 0 {
			return fmt.Errorf("backup_retention_count must be a non-negative integer")
		}
		m.mu.Lock()
		m.retention.Count = n
		m.mu.Unlock()
		return nil
	case "backup_retention_days", "BACKUP_RETENTION_DAYS":
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || n < 0 {
			return fmt.Errorf("backup_retention_days must be a non-negative integer")
		}
		m.mu.Lock()
		m.retention.Days = n
		m.mu.Unlock()
		return nil
	default:
		return fmt.Errorf("unknown setting %q", key)
	}
}
