package internal

import (
	"context"
	"os"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	backupv1 "github.com/Muxcore-Media/backup-local/muxcore/backup/v1"
)

// VerifyBackup checks an archive's SHA-256 against the index and, when
// restore_test is set, extracts it into a throwaway directory.
func (m *Module) VerifyBackup(ctx context.Context, req *backupv1.VerifyBackupRequest) (*backupv1.VerifyBackupResponse, error) {
	id := req.GetBackupId()
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "backup_id is required")
	}
	m.mu.Lock()
	_, ok := m.backups[id]
	m.mu.Unlock()
	if !ok {
		m.rescanIndex()
	}
	m.mu.Lock()
	meta, ok := m.backups[id]
	dir := m.dir
	m.mu.Unlock()
	if !ok {
		return nil, status.Error(codes.NotFound, "backup not found")
	}
	path := archivePath(dir, meta.ID)
	sum, size, err := sha256File(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, status.Error(codes.NotFound, "backup archive missing on disk")
		}
		return nil, status.Errorf(codes.Internal, "checksum backup: %v", err)
	}
	resp := &backupv1.VerifyBackupResponse{
		ChecksumSha256: sum,
		SizeBytes:      size,
		ChecksumOk:     sum == meta.Checksum,
	}
	resp.Valid = resp.ChecksumOk
	if !resp.ChecksumOk {
		resp.Message = "checksum mismatch"
		return resp, nil
	}
	if req.GetRestoreTest() {
		tmp, err := os.MkdirTemp("", "backup-verify-*")
		if err != nil {
			return nil, status.Errorf(codes.Internal, "temp dir: %v", err)
		}
		defer func() { _ = os.RemoveAll(tmp) }()
		files, _, err := extractTarGz(path, tmp)
		resp.FilesInArchive = files
		if err != nil {
			resp.Valid = false
			resp.Message = "restore test failed: " + err.Error()
			return resp, nil
		}
		resp.RestoreTestOk = true
	}
	resp.Message = "ok"
	return resp, nil
}
