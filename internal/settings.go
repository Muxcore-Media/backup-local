package internal

import (
	"fmt"
	"os"
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
	default:
		return fmt.Errorf("unknown setting %q", key)
	}
}
