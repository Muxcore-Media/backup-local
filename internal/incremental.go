package internal

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type fileManifestEntry struct {
	Size        int64 `json:"size"`
	ModTimeNano int64 `json:"mod_time_nano"`
}

func buildSourceManifest(sources []string, excludeGlobs []string) (map[string]fileManifestEntry, error) {
	manifest := make(map[string]fileManifestEntry)
	usedPrefixes := make(map[string]int)
	for _, src := range sources {
		if strings.TrimSpace(src) == "" {
			continue
		}
		prefix := filepath.ToSlash(filepath.Join("data", uniqueSourcePrefix(src, usedPrefixes)))
		if err := walkSourceManifest(src, prefix, excludeGlobs, manifest); err != nil {
			return nil, err
		}
	}
	return manifest, nil
}

func walkSourceManifest(root, tarPrefix string, excludeGlobs []string, manifest map[string]fileManifestEntry) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	info, err := os.Stat(rootAbs)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		name := filepath.ToSlash(filepath.Join(tarPrefix, filepath.Base(rootAbs)))
		manifest[name] = fileManifestEntry{Size: info.Size(), ModTimeNano: info.ModTime().UnixNano()}
		return nil
	}
	return filepath.Walk(rootAbs, func(path string, fi os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if fi.IsDir() || !fi.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(rootAbs, path)
		if err != nil {
			return err
		}
		if shouldExclude(rel, excludeGlobs) {
			return nil
		}
		name := filepath.ToSlash(filepath.Join(tarPrefix, rel))
		manifest[name] = fileManifestEntry{Size: fi.Size(), ModTimeNano: fi.ModTime().UnixNano()}
		return nil
	})
}

func manifestDiff(parent, current map[string]fileManifestEntry) map[string]struct{} {
	changed := make(map[string]struct{})
	for path, cur := range current {
		prev, ok := parent[path]
		if !ok || prev.Size != cur.Size || prev.ModTimeNano != cur.ModTimeNano {
			changed[path] = struct{}{}
		}
	}
	return changed
}

func mergeManifest(parent, current map[string]fileManifestEntry) map[string]fileManifestEntry {
	out := make(map[string]fileManifestEntry, len(parent)+len(current))
	for k, v := range parent {
		out[k] = v
	}
	for k, v := range current {
		out[k] = v
	}
	return out
}

func (m *Module) latestBackupMeta() (backupMeta, bool) {
	var latest backupMeta
	var found bool
	for _, b := range m.backups {
		if !found || b.Timestamp > latest.Timestamp {
			latest = b
			found = true
		}
	}
	return latest, found
}

func (m *Module) resolveRestoreChain(backupID string) ([]string, error) {
	var chain []string
	seen := make(map[string]struct{})
	for id := backupID; id != ""; {
		if _, ok := seen[id]; ok {
			return nil, fmt.Errorf("backup parent chain cycle at %q", id)
		}
		seen[id] = struct{}{}
		meta, ok := m.backups[id]
		if !ok {
			return nil, fmt.Errorf("backup %q not found in chain", id)
		}
		chain = append([]string{id}, chain...)
		id = meta.ParentID
	}
	return chain, nil
}
