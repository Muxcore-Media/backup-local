package internal

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func parseAllowedRoots(sources []string, env string) []string {
	seen := make(map[string]struct{})
	var roots []string
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" {
			return
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			abs = filepath.Clean(p)
		}
		abs = filepath.Clean(abs)
		if _, ok := seen[abs]; ok {
			return
		}
		seen[abs] = struct{}{}
		roots = append(roots, abs)
	}
	for _, s := range sources {
		add(s)
	}
	if env != "" {
		for _, p := range splitCSV(env) {
			add(p)
		}
	}
	return roots
}

func pathUnderAllowedRoot(path string, roots []string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	abs = filepath.Clean(abs)
	if len(roots) == 0 {
		return "", fmt.Errorf("path %q is not under any allowed backup root (configure BACKUP_SOURCE_DIRS or BACKUP_ALLOWED_ROOTS)", abs)
	}
	for _, root := range roots {
		root = filepath.Clean(root)
		rel, err := filepath.Rel(root, abs)
		if err != nil {
			continue
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			continue
		}
		return abs, nil
	}
	return "", fmt.Errorf("path %q is outside allowed backup roots", abs)
}

func validateSourcePath(path string, roots []string) (string, error) {
	return pathUnderAllowedRoot(path, roots)
}

func validateTargetPath(path string, roots []string) (string, error) {
	return pathUnderAllowedRoot(path, roots)
}
