package internal

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	backupv1 "github.com/Muxcore-Media/backup-local/muxcore/backup/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (m *Module) VerifyBackup(ctx context.Context, req *backupv1.VerifyBackupRequest) (*backupv1.VerifyBackupResponse, error) {
	start := time.Now()
	backupID := req.GetBackupId()
	if backupID == "" {
		return nil, status.Error(codes.InvalidArgument, "backup_id is required")
	}
	if err := validateBackupID(backupID); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}

	m.mu.Lock()
	meta, ok := m.backups[backupID]
	dir := m.dir
	cryptor := m.cryptor
	m.mu.Unlock()
	if !ok {
		return nil, status.Error(codes.NotFound, "backup not found")
	}

	path := archivePath(dir, meta.ID)
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, status.Error(codes.NotFound, "backup archive missing on disk")
		}
		return nil, status.Errorf(codes.Internal, "stat archive: %v", err)
	}

	resp := &backupv1.VerifyBackupResponse{
		SizeBytes: info.Size(),
	}

	sum, size, err := sha256File(path)
	if err != nil {
		logBackupWarn("verify", backupID, start, err)
		return nil, status.Errorf(codes.Internal, "checksum archive: %v", err)
	}
	resp.ChecksumSha256 = sum
	resp.SizeBytes = size
	resp.ChecksumOk = meta.Checksum == "" || sum == meta.Checksum
	if !resp.ChecksumOk {
		resp.Message = "checksum mismatch"
		logBackupOp("verify", backupID, start, size, "checksum_ok", false)
		return resp, nil
	}

	if req.GetRestoreTest() {
		files, testErr := m.restoreTestExtract(ctx, cryptor, path)
		resp.FilesInArchive = files
		resp.RestoreTestOk = testErr == nil
		if testErr != nil {
			resp.Message = fmt.Sprintf("restore test failed: %v", testErr)
			logBackupWarn("verify", backupID, start, testErr, "restore_test", false)
			return resp, nil
		}
	}

	resp.Valid = resp.ChecksumOk && (!req.GetRestoreTest() || resp.RestoreTestOk)
	if resp.Message == "" {
		resp.Message = "ok"
	}
	logBackupOp("verify", backupID, start, size, "checksum_ok", true, "restore_test", req.GetRestoreTest(), "valid", resp.Valid)
	return resp, nil
}

func (m *Module) restoreTestExtract(ctx context.Context, cryptor *archiveCryptor, archivePath string) (int64, error) {
	workArchive, cleanup, err := decryptArchiveToTemp(ctx, cryptor, archivePath)
	if err != nil {
		return 0, err
	}
	defer cleanup()

	staging, err := os.MkdirTemp(filepath.Dir(archivePath), ".backup-verify-*")
	if err != nil {
		return 0, err
	}
	defer func() { _ = os.RemoveAll(staging) }()

	files, _, err := extractTarGzToDir(workArchive, staging)
	return files, err
}

func (m *Module) verifyAfterCreate(ctx context.Context, backupID string, restoreTest bool) error {
	resp, err := m.VerifyBackup(ctx, &backupv1.VerifyBackupRequest{
		BackupId:    backupID,
		RestoreTest: restoreTest,
	})
	if err != nil {
		return err
	}
	if !resp.GetValid() {
		return fmt.Errorf("%s", resp.GetMessage())
	}
	return nil
}
