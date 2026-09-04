package internal

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

func archivePath(dir, id string) string {
	return filepath.Join(dir, id+".tar.gz")
}

func sha256File(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = f.Close() }()
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

func writeStreamingTarGz(ctx context.Context, path string, sources []string, peers map[string]contracts.Backupable, peerIDs []string, excludeGlobs []string, onlyInclude map[string]struct{}) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)

	usedPrefixes := make(map[string]int)
	for _, src := range sources {
		if strings.TrimSpace(src) == "" {
			continue
		}
		prefix := filepath.ToSlash(filepath.Join("data", uniqueSourcePrefix(src, usedPrefixes)))
		if err := appendDirToTar(ctx, tw, src, prefix, excludeGlobs, onlyInclude); err != nil {
			_ = tw.Close()
			_ = gw.Close()
			return err
		}
	}

	for _, id := range peerIDs {
		select {
		case <-ctx.Done():
			_ = tw.Close()
			_ = gw.Close()
			return ctx.Err()
		default:
		}
		peer, ok := peers[id]
		if !ok {
			_ = tw.Close()
			_ = gw.Close()
			return fmt.Errorf("backupable peer %q not registered", id)
		}
		data, err := peer.ExportState(ctx)
		if err != nil {
			_ = tw.Close()
			_ = gw.Close()
			return fmt.Errorf("export %q: %w", id, err)
		}
		name := filepath.ToSlash(filepath.Join("modules", id, "state.bin"))
		if err := writeTarBytes(tw, name, data, 0600); err != nil {
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

func appendDirToTar(ctx context.Context, tw *tar.Writer, root, tarPrefix string, excludeGlobs []string, onlyInclude map[string]struct{}) error {
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
		if onlyInclude != nil {
			if _, ok := onlyInclude[name]; !ok {
				return nil
			}
		}
		data, err := os.ReadFile(rootAbs)
		if err != nil {
			return err
		}
		return writeTarBytes(tw, name, data, 0600)
	}

	return filepath.Walk(rootAbs, func(path string, fi os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
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
		if shouldExclude(rel, excludeGlobs) {
			return nil
		}
		name := filepath.ToSlash(filepath.Join(tarPrefix, rel))
		if onlyInclude != nil {
			if _, ok := onlyInclude[name]; !ok {
				return nil
			}
		}
		mode := restrictFileMode(int64(fi.Mode().Perm()))
		return writeTarFile(ctx, tw, path, name, fi.Size(), mode)
	})
}

func writeTarFile(ctx context.Context, tw *tar.Writer, srcPath, tarName string, size int64, mode int64) error {
	hdr := &tar.Header{Name: tarName, Mode: mode, Size: size}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, 32*1024)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		n, readErr := f.Read(buf)
		if n > 0 {
			if _, err := tw.Write(buf[:n]); err != nil {
				return err
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	return nil
}

func writeTarBytes(tw *tar.Writer, name string, data []byte, mode int64) error {
	hdr := &tar.Header{Name: name, Mode: mode, Size: int64(len(data))}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}

func extractTarGzToDir(archive, target string) (int64, map[string][]byte, error) {
	f, err := os.Open(archive)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = f.Close() }()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = gr.Close() }()

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
		case tar.TypeReg:
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

func atomicRestoreTree(staging, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return err
	}
	backup := target + ".backup-pre-restore"
	_ = os.RemoveAll(backup)
	if _, err := os.Stat(target); err == nil {
		if err := os.Rename(target, backup); err != nil {
			return fmt.Errorf("rename existing target: %w", err)
		}
		defer func() { _ = os.RemoveAll(backup) }()
	}
	if err := os.Rename(staging, target); err != nil {
		if _, statErr := os.Stat(backup); statErr == nil {
			_ = os.Rename(backup, target)
		}
		return fmt.Errorf("promote staging tree: %w", err)
	}
	return nil
}

// writeEvilArchive kept for traversal tests.
func writeEvilArchive(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	payload := []byte("pwned")
	hdr := &tar.Header{
		Name: "../escape.txt",
		Mode: 0600,
		Size: int64(len(payload)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	if _, err := tw.Write(payload); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gw.Close()
}
