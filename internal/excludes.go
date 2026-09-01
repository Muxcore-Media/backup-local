package internal

import (
	"path/filepath"
	"strings"
)

var defaultExcludeGlobs = []string{".incomplete", "*.parts", "*.part", "*.!qb", "*.tmp"}

func parseExcludeGlobs(env string, configured string) []string {
	if strings.TrimSpace(configured) != "" {
		return splitCSV(configured)
	}
	if strings.TrimSpace(env) != "" {
		return splitCSV(env)
	}
	out := make([]string, len(defaultExcludeGlobs))
	copy(out, defaultExcludeGlobs)
	return out
}

func shouldExclude(relPath string, globs []string) bool {
	if len(globs) == 0 {
		return false
	}
	base := filepath.Base(relPath)
	for _, g := range globs {
		g = strings.TrimSpace(g)
		if g == "" {
			continue
		}
		if matched, _ := filepath.Match(g, base); matched {
			return true
		}
		if matched, _ := filepath.Match(g, relPath); matched {
			return true
		}
		if matched, _ := filepath.Match(g, filepath.ToSlash(relPath)); matched {
			return true
		}
	}
	return false
}
