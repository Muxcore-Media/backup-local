package internal

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func archivePath(dir, id string) string {
	return filepath.Join(dir, id+".tar.gz")
}

func sha256File(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func safeJoin(root, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("empty archive entry name")
	}
	clean := filepath.Clean(name)
	clean = strings.TrimPrefix(clean, string(os.PathSeparator))
	if clean == "." || clean == "" {
		return "", fmt.Errorf("invalid archive entry name %q", name)
	}
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("archive entry escapes target: %q", name)
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve target: %w", err)
	}
	rootAbs = filepath.Clean(rootAbs)
	dest := filepath.Clean(filepath.Join(rootAbs, clean))
	rel, err := filepath.Rel(rootAbs, dest)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("archive entry escapes target: %q", name)
	}
	return dest, nil
}

func validateTargetDir(target string) (string, error) {
	if strings.TrimSpace(target) == "" {
		return "", fmt.Errorf("target_path is required")
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return "", fmt.Errorf("resolve target_path: %w", err)
	}
	return filepath.Clean(abs), nil
}

type tarEntry struct {
	Name string
	Data []byte
	Mode int64
}

func writeTarGz(path string, entries []tarEntry) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()

	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)

	for _, e := range entries {
		hdr := &tar.Header{
			Name: e.Name,
			Mode: e.Mode,
			Size: int64(len(e.Data)),
		}
		if hdr.Mode == 0 {
			hdr.Mode = 0600
		}
		if err := tw.WriteHeader(hdr); err != nil {
			_ = tw.Close()
			_ = gw.Close()
			return err
		}
		if _, err := tw.Write(e.Data); err != nil {
			_ = tw.Close()
			_ = gw.Close()
			return err
		}
	}

	if err := tw.Close(); err != nil {
		_ = gw.Close()
		return err
	}
	return gw.Close()
}

func collectDirEntries(root, tarPrefix string) ([]tarEntry, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(rootAbs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		data, err := os.ReadFile(rootAbs)
		if err != nil {
			return nil, err
		}
		return []tarEntry{{Name: filepath.ToSlash(filepath.Join(tarPrefix, filepath.Base(rootAbs))), Data: data, Mode: 0600}}, nil
	}

	var entries []tarEntry
	err = filepath.Walk(rootAbs, func(path string, fi os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if fi.IsDir() {
			return nil
		}
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("refusing non-regular file %q", path)
		}
		rel, err := filepath.Rel(rootAbs, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(filepath.Join(tarPrefix, rel))
		mode := int64(fi.Mode().Perm())
		if mode == 0 {
			mode = 0600
		}
		entries = append(entries, tarEntry{Name: name, Data: data, Mode: mode})
		return nil
	})
	return entries, err
}

func extractTarGz(archive, target string) (int64, map[string][]byte, error) {
	f, err := os.Open(archive)
	if err != nil {
		return 0, nil, err
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return 0, nil, err
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	var files int64
	moduleStates := make(map[string][]byte)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return files, moduleStates, err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			dest, err := safeJoin(target, hdr.Name)
			if err != nil {
				return files, moduleStates, err
			}
			if err := os.MkdirAll(dest, 0700); err != nil {
				return files, moduleStates, err
			}
		case tar.TypeReg, tar.TypeRegA:
			dest, err := safeJoin(target, hdr.Name)
			if err != nil {
				return files, moduleStates, err
			}
			if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
				return files, moduleStates, err
			}
			out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
			if err != nil {
				return files, moduleStates, err
			}
			n, copyErr := io.Copy(out, tr)
			closeErr := out.Close()
			if copyErr != nil {
				return files, moduleStates, copyErr
			}
			if closeErr != nil {
				return files, moduleStates, closeErr
			}
			if n != hdr.Size {
				return files, moduleStates, fmt.Errorf("short write for %q", hdr.Name)
			}
			files++

			if id, ok := moduleStateID(hdr.Name); ok {
				data, err := os.ReadFile(dest)
				if err != nil {
					return files, moduleStates, err
				}
				moduleStates[id] = data
			}
		default:
			return files, moduleStates, fmt.Errorf("unsupported archive entry type %q for %q", string(hdr.Typeflag), hdr.Name)
		}
	}
	return files, moduleStates, nil
}

func moduleStateID(name string) (string, bool) {
	clean := filepath.ToSlash(filepath.Clean(name))
	parts := strings.Split(clean, "/")
	if len(parts) != 3 || parts[0] != "modules" || parts[2] != "state.bin" || parts[1] == "" || parts[1] == "." || parts[1] == ".." {
		return "", false
	}
	if strings.ContainsAny(parts[1], `/\`) {
		return "", false
	}
	return parts[1], true
}

func uniqueSourcePrefix(path string, used map[string]int) string {
	base := filepath.Base(filepath.Clean(path))
	if base == "" || base == "." || base == string(os.PathSeparator) {
		base = "source"
	}
	n := used[base]
	used[base] = n + 1
	if n == 0 {
		return base
	}
	return fmt.Sprintf("%s_%d", base, n+1)
}
