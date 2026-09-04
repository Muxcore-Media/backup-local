package internal

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

func (m *Module) loadIndex() error {
	path := filepath.Join(m.dir, "index.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return m.reconcileIndex(false)
		}
		return err
	}
	var list []backupMeta
	if err := json.Unmarshal(data, &list); err != nil {
		// Keep any in-memory entries; rebuild from archives on disk.
		return m.reconcileIndex(true)
	}
	for _, b := range list {
		m.backups[b.ID] = b
	}
	return m.reconcileIndex(false)
}

func (m *Module) reconcileIndex(indexCorrupt bool) error {
	entries, err := filepath.Glob(filepath.Join(m.dir, "*.tar.gz"))
	if err != nil {
		return err
	}
	onDisk := make(map[string]struct{}, len(entries))
	for _, p := range entries {
		base := filepath.Base(p)
		id := strings.TrimSuffix(base, ".tar.gz")
		if id == "" {
			continue
		}
		if err := validateBackupID(id); err != nil {
			slog.Warn("backup-local: skipping archive with invalid id", "path", p, "error", err)
			continue
		}
		onDisk[id] = struct{}{}
		if _, ok := m.backups[id]; ok {
			continue
		}
		sum, size, err := sha256File(p)
		if err != nil {
			slog.Warn("backup-local: reconcile archive checksum failed", "path", p, "error", err)
			continue
		}
		fi, err := os.Stat(p)
		if err != nil {
			slog.Warn("backup-local: reconcile archive stat failed", "path", p, "error", err)
			continue
		}
		m.backups[id] = backupMeta{
			ID:        id,
			Timestamp: fi.ModTime().Unix(),
			Size:      size,
			Checksum:  sum,
		}
	}
	for id := range m.backups {
		if _, ok := onDisk[id]; !ok {
			delete(m.backups, id)
		}
	}
	if indexCorrupt && len(onDisk) > 0 {
		if err := m.saveIndexLocked(); err != nil {
			return fmt.Errorf("reconcile corrupt index: %w", err)
		}
	}
	return nil
}

func (m *Module) saveIndex() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.saveIndexLocked()
}

func (m *Module) saveIndexLocked() error {
	list := make([]backupMeta, 0, len(m.backups))
	for _, b := range m.backups {
		list = append(list, b)
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal backup index: %w", err)
	}
	path := filepath.Join(m.dir, "index.json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("write backup index tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename backup index: %w", err)
	}
	return nil
}

func (m *Module) indexReadable() error {
	path := filepath.Join(m.dir, "index.json")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var list []backupMeta
	return json.Unmarshal(data, &list)
}

func dirWritable(dir string) error {
	test := filepath.Join(dir, ".health-write-test")
	if err := os.WriteFile(test, []byte("ok"), 0600); err != nil {
		return err
	}
	return os.Remove(test)
}
