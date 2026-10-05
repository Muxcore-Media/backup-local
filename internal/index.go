package internal

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// archiveNameRE is the only filename shape the rescan accepts. It mirrors the
// "backup_<unix-nanos>" IDs minted by CreateBackup; nothing else from a
// filename is ever used as a path component.
var archiveNameRE = regexp.MustCompile(`^(backup_([0-9]{1,19}))\.tar\.gz$`)

// rescanIndex adds an index entry for every backup_*.tar.gz in the backup
// directory that index.json does not list (for example an archive copied into
// a fresh install's BACKUP_DIR). Recovered entries are flagged Recovered and
// the index is persisted when anything was added. It returns the number of
// entries added.
func (m *Module) rescanIndex() int {
	m.mu.Lock()
	dir := m.dir
	known := make(map[string]bool, len(m.backups))
	for id := range m.backups {
		known[id] = true
	}
	m.mu.Unlock()

	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("backup index rescan: read dir", "error", err)
		}
		return 0
	}

	var recovered []backupMeta
	for _, de := range dirEntries {
		sub := archiveNameRE.FindStringSubmatch(de.Name())
		if sub == nil || known[sub[1]] {
			continue
		}
		// Lstat on the joined, regex-validated name: refuse symlinks/non-regular.
		path := filepath.Join(dir, de.Name())
		fi, err := os.Lstat(path)
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		sum, size, err := sha256File(path)
		if err != nil {
			slog.Warn("backup index rescan: checksum", "file", de.Name(), "error", err)
			continue
		}
		ts := fi.ModTime().Unix()
		if nanos, err := strconv.ParseInt(sub[2], 10, 64); err == nil && nanos > 0 {
			ts = nanos / 1e9
		}
		moduleIDs, err := archiveModuleIDs(path)
		if err != nil {
			slog.Warn("backup index rescan: unreadable archive, skipping", "file", de.Name(), "error", err)
			continue
		}
		recovered = append(recovered, backupMeta{
			ID:        sub[1],
			Timestamp: ts,
			Size:      size,
			Checksum:  sum,
			ModuleIDs: moduleIDs,
			Recovered: true,
		})
		slog.Info("backup recovered into index", "id", sub[1], "size", size, "checksum", sum)
	}
	if len(recovered) == 0 {
		return 0
	}

	m.mu.Lock()
	added := 0
	for _, b := range recovered {
		if _, ok := m.backups[b.ID]; !ok {
			m.backups[b.ID] = b
			added++
		}
	}
	if added > 0 {
		m.saveIndex()
	}
	m.mu.Unlock()
	return added
}

// archiveModuleIDs lists the module IDs whose state.bin the archive carries
// (the only metadata archives embed; there is no manifest).
func archiveModuleIDs(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	gr, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer func() { _ = gr.Close() }()
	tr := tar.NewReader(gr)
	ids := []string{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if id, ok := moduleStateID(hdr.Name); ok && !strings.ContainsAny(id, `/\`) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids, nil
}
