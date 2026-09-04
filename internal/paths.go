package internal

import (
	"fmt"
	"log/slog"
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
	if hasPathTraversal(path) {
		return "", fmt.Errorf("path must not contain traversal (%q)", path)
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

// validateBackupDir resolves and validates a backup storage directory.
func validateBackupDir(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("backup_dir must not be empty")
	}
	if hasPathTraversal(path) {
		return "", fmt.Errorf("backup_dir must not contain path traversal (%q)", path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve backup_dir: %w", err)
	}
	abs = filepath.Clean(abs)
	if hasPathTraversal(abs) {
		return "", fmt.Errorf("backup_dir resolves outside allowed path (%q)", abs)
	}
	return abs, nil
}

// validateBackupID ensures archive filenames cannot escape the backup directory.
func validateBackupID(id string) error {
	if id == "" {
		return fmt.Errorf("backup id is required")
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-':
		default:
			return fmt.Errorf("invalid backup id %q", id)
		}
	}
	if id == "." || id == ".." {
		return fmt.Errorf("invalid backup id %q", id)
	}
	return nil
}

// validateSourceDirs checks configured source directories exist and sit under allowed roots.
func validateSourceDirs(paths []string, allowed []string) error {
	if len(paths) == 0 {
		return nil
	}
	if len(allowed) == 0 {
		return fmt.Errorf("source_dirs requires BACKUP_SOURCE_DIRS or BACKUP_ALLOWED_ROOTS")
	}
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if hasPathTraversal(p) {
			return fmt.Errorf("source_dirs entry must not contain path traversal (%q)", p)
		}
		validated, err := validateSourcePath(p, allowed)
		if err != nil {
			return fmt.Errorf("source_dirs: %w", err)
		}
		info, err := os.Stat(validated)
		if err != nil {
			return fmt.Errorf("source_dirs: stat %q: %w", validated, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("source_dirs: %q is not a directory", validated)
		}
	}
	return nil
}

func hasPathTraversal(path string) bool {
	clean := filepath.Clean(path)
	if clean == ".." {
		return true
	}
	for _, part := range strings.Split(clean, string(os.PathSeparator)) {
		if part == ".." {
			return true
		}
	}
	return false
}

// restrictFileMode limits archive entry permissions to owner read/write.
func restrictFileMode(mode int64) int64 {
	if mode == 0 {
		return 0600
	}
	return mode & 0600
}

// ensureDirPerm sets directory permissions when the existing mode is too permissive.
func ensureDirPerm(path string, perm os.FileMode) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() && info.Mode().Perm()&0077 != 0 {
		if err := os.Chmod(path, perm); err != nil {
			slog.Warn("backup-local: tighten directory permissions failed", "path", path, "error", err)
		}
	}
	return nil
}

// ensureFilePerm sets file permissions when the existing mode is too permissive.
func ensureFilePerm(path string, perm os.FileMode) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() && info.Mode().Perm()&0077 != 0 {
		if err := os.Chmod(path, perm); err != nil {
			slog.Warn("backup-local: tighten file permissions failed", "path", path, "error", err)
		}
	}
	return nil
}
