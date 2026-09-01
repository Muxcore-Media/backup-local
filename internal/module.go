package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	backupv1 "github.com/Muxcore-Media/backup-local/muxcore/backup/v1"
	"github.com/Muxcore-Media/core/pkg/contracts"
	"github.com/Muxcore-Media/core/sdk/go/client"
	modulesdk "github.com/Muxcore-Media/core/sdk/go/module"
)

type backupMeta struct {
	ID        string   `json:"id"`
	Timestamp int64    `json:"timestamp"`
	Size      int64    `json:"size"`
	Checksum  string   `json:"checksum_sha256"`
	ModuleIDs []string `json:"module_ids"`
	Encrypted bool     `json:"encrypted,omitempty"`
}

// BackupablePeer is a named Backupable used when exporting/importing module state.
type BackupablePeer struct {
	ID      string
	Backend contracts.Backupable
}

type Module struct {
	backupv1.UnimplementedBackupServiceServer
	mu              sync.Mutex
	dir             string
	sources         []string
	allowedRoots    []string
	excludeGlobs    []string
	maxBackups      int
	maxAgeDays      int
	scheduleCronVal string
	peers           map[string]contracts.Backupable
	peerConns       []*grpcBackupable
	backups         map[string]backupMeta
	cryptor         *archiveCryptor
	mc              *client.Client
	encConn         *grpc.ClientConn
	grpcSrv         *grpc.Server
	grpcLis         net.Listener
	httpLis         net.Listener
	cancel          context.CancelFunc
	id              string
	grpcAddr        string
	httpAddr        string
}

type Config struct {
	ID           string
	Dir          string
	SourceDirs   []string
	AllowedRoots []string
	ExcludeGlobs []string
	MaxBackups   int
	MaxAgeDays   int
	ScheduleCron string
	EncryptKey   string
	Peers        []BackupablePeer
	GRPCAddr     string
	HTTPAddr     string
}

func NewModule(cfg Config) *Module {
	if cfg.ID == "" {
		cfg.ID = "backup-local"
	}
	if cfg.Dir == "" {
		cfg.Dir = "backups"
	}
	if cfg.GRPCAddr == "" {
		cfg.GRPCAddr = ":9302"
	}
	if cfg.HTTPAddr == "" {
		cfg.HTTPAddr = ":9303"
	}
	if v := os.Getenv("BACKUP_DIR"); v != "" {
		cfg.Dir = v
	}
	if v := os.Getenv("BACKUP_SOURCE_DIRS"); v != "" {
		cfg.SourceDirs = splitCSV(v)
	}
	if v := os.Getenv("BACKUP_ALLOWED_ROOTS"); v != "" {
		cfg.AllowedRoots = splitCSV(v)
	}
	if v := os.Getenv("BACKUP_EXCLUDE"); v != "" {
		cfg.ExcludeGlobs = splitCSV(v)
	}
	if v := os.Getenv("BACKUP_SCHEDULE"); v != "" && cfg.ScheduleCron == "" {
		cfg.ScheduleCron = v
	}
	if v := os.Getenv("BACKUP_ENCRYPT_KEY"); v != "" && cfg.EncryptKey == "" {
		cfg.EncryptKey = v
	}
	peers := make(map[string]contracts.Backupable, len(cfg.Peers))
	for _, p := range cfg.Peers {
		if p.ID == "" || p.Backend == nil {
			continue
		}
		peers[p.ID] = p.Backend
	}
	sources := append([]string(nil), cfg.SourceDirs...)
	excludeGlobs := parseExcludeGlobs(os.Getenv("BACKUP_EXCLUDE"), strings.Join(cfg.ExcludeGlobs, ","))
	allowed := parseAllowedRoots(sources, os.Getenv("BACKUP_ALLOWED_ROOTS"))
	if len(cfg.AllowedRoots) > 0 {
		allowed = parseAllowedRoots(append(sources, cfg.AllowedRoots...), "")
	}
	cryptor, _ := newArchiveCryptor(cfg.EncryptKey, nil)
	return &Module{
		id:              cfg.ID,
		dir:             cfg.Dir,
		sources:         sources,
		allowedRoots:    allowed,
		excludeGlobs:    excludeGlobs,
		maxBackups:      cfg.MaxBackups,
		maxAgeDays:      cfg.MaxAgeDays,
		scheduleCronVal: cfg.ScheduleCron,
		peers:           peers,
		backups:         make(map[string]backupMeta),
		cryptor:         cryptor,
		grpcAddr:        cfg.GRPCAddr,
		httpAddr:        cfg.HTTPAddr,
	}
}

func splitCSV(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (m *Module) Info() contracts.ModuleInfo {
	return contracts.ModuleInfo{
		ID:           m.id,
		Name:         "Backup Local",
		Version:      "0.2.0",
		Roles:        []string{"infrastructure"},
		Description:  "Local filesystem backup/restore provider",
		Author:       "MuxCore",
		Capabilities: []string{contracts.CapabilityBackup, "backup.local", "settings"},
		HTTPAddr:     m.httpAddr,
	}
}

func (m *Module) Init(ctx context.Context) error {
	if err := os.MkdirAll(m.dir, 0700); err != nil {
		return fmt.Errorf("create backup dir: %w", err)
	}
	if err := m.loadIndex(); err != nil {
		slog.Warn("backup index reconcile issue", "error", err)
	}
	var err error
	m.grpcLis, err = net.Listen("tcp", m.grpcAddr)
	if err != nil {
		return fmt.Errorf("listen gRPC %s: %w", m.grpcAddr, err)
	}
	m.httpLis, err = net.Listen("tcp", m.httpAddr)
	if err != nil {
		return fmt.Errorf("listen HTTP %s: %w", m.httpAddr, err)
	}
	slog.Info("backup-local initialized", "dir", m.dir, "backups", len(m.backups))
	return nil
}

func (m *Module) Start(ctx context.Context) error {
	loopCtx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	go m.dialCoreLoop(loopCtx)
	m.startScheduleLoop(loopCtx)

	m.grpcSrv = grpc.NewServer()
	backupv1.RegisterBackupServiceServer(m.grpcSrv, m)
	modulesdk.RegisterSettings(m.grpcSrv, m.id, m)
	go func() {
		slog.Info("backup gRPC started", "addr", m.grpcAddr)
		if err := m.grpcSrv.Serve(m.grpcLis); err != nil {
			slog.Error("backup gRPC error", "error", err)
		}
	}()
	mux := http.NewServeMux()
	mux.HandleFunc("/health", m.handleHTTPHealth)
	go func() {
		slog.Info("backup HTTP started", "addr", m.httpAddr)
		_ = http.Serve(m.httpLis, mux)
	}()
	return nil
}

func (m *Module) Stop(ctx context.Context) error {
	if m.cancel != nil {
		m.cancel()
	}
	if m.grpcSrv != nil {
		m.grpcSrv.GracefulStop()
	}
	if m.httpLis != nil {
		_ = m.httpLis.Close()
	}
	m.mu.Lock()
	for _, c := range m.peerConns {
		_ = c.Close()
	}
	m.peerConns = nil
	if m.mc != nil {
		_ = m.mc.Close()
		m.mc = nil
	}
	if m.encConn != nil {
		_ = m.encConn.Close()
		m.encConn = nil
	}
	m.mu.Unlock()
	slog.Info("backup-local stopped")
	return nil
}

func (m *Module) Health(ctx context.Context) error {
	m.mu.Lock()
	dir := m.dir
	m.mu.Unlock()
	if err := m.checkStorageHealth(dir); err != nil {
		return err
	}
	return nil
}

func (m *Module) checkStorageHealth(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("backup dir: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("backup dir is not a directory")
	}
	if err := dirWritable(dir); err != nil {
		return fmt.Errorf("backup dir not writable: %w", err)
	}
	if err := m.indexReadable(); err != nil {
		return fmt.Errorf("backup index unreadable: %w", err)
	}
	return nil
}

func (m *Module) handleHTTPHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if err := m.checkStorageHealth(m.dir); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":"degraded","error":` + jsonString(err.Error()) + `}`))
		return
	}
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func (m *Module) CreateBackup(ctx context.Context, req *backupv1.CreateBackupRequest) (*backupv1.CreateBackupResponse, error) {
	if req == nil {
		req = &backupv1.CreateBackupRequest{}
	}
	m.mu.Lock()
	sources := append([]string(nil), m.sources...)
	dir := m.dir
	allowed := append([]string(nil), m.allowedRoots...)
	excludeGlobs := append([]string(nil), m.excludeGlobs...)
	peers := make(map[string]contracts.Backupable, len(m.peers))
	for id, p := range m.peers {
		peers[id] = p
	}
	cryptor := m.cryptor
	m.mu.Unlock()

	for _, src := range req.GetSourcePaths() {
		validated, err := validateSourcePath(src, allowed)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "source_paths: %v", err)
		}
		sources = append(sources, validated)
	}

	peerIDs := req.GetModuleIds()
	if len(peerIDs) == 0 {
		for id := range peers {
			peerIDs = append(peerIDs, id)
		}
		sort.Strings(peerIDs)
	}

	includedModules := append([]string(nil), peerIDs...)
	hasSources := false
	for _, src := range sources {
		if strings.TrimSpace(src) != "" {
			hasSources = true
			break
		}
	}
	if !hasSources && len(includedModules) == 0 {
		return nil, status.Error(codes.FailedPrecondition, "nothing to back up: configure BACKUP_SOURCE_DIRS, pass source_paths, or register Backupable peers")
	}

	id := fmt.Sprintf("backup_%d", time.Now().UnixNano())
	plainPath := archivePath(dir, id) + ".plain"
	path := archivePath(dir, id)
	if err := writeStreamingTarGz(ctx, plainPath, sources, peers, includedModules, excludeGlobs); err != nil {
		_ = os.Remove(plainPath)
		if strings.Contains(err.Error(), "not registered") {
			return nil, status.Errorf(codes.NotFound, "%v", err)
		}
		return nil, status.Errorf(codes.Internal, "write backup: %v", err)
	}

	encrypted := false
	if cryptor != nil && cryptor.enabled() {
		if err := cryptor.encryptFile(ctx, plainPath, path); err != nil {
			_ = os.Remove(plainPath)
			return nil, status.Errorf(codes.Internal, "encrypt backup: %v", err)
		}
		_ = os.Remove(plainPath)
		encrypted = true
	} else {
		if err := os.Rename(plainPath, path); err != nil {
			_ = os.Remove(plainPath)
			return nil, status.Errorf(codes.Internal, "finalize backup: %v", err)
		}
	}

	sum, size, err := sha256File(path)
	if err != nil {
		_ = os.Remove(path)
		return nil, status.Errorf(codes.Internal, "checksum backup: %v", err)
	}

	meta := backupMeta{
		ID:        id,
		Timestamp: time.Now().Unix(),
		Size:      size,
		Checksum:  sum,
		ModuleIDs: includedModules,
		Encrypted: encrypted,
	}

	m.mu.Lock()
	m.backups[id] = meta
	err = m.saveIndexLocked()
	m.mu.Unlock()
	if err != nil {
		_ = os.Remove(path)
		m.mu.Lock()
		delete(m.backups, id)
		m.mu.Unlock()
		return nil, status.Errorf(codes.Internal, "persist index: %v", err)
	}

	if err := m.pruneBackups(ctx); err != nil {
		slog.Warn("backup-local: prune after create", "error", err)
	}

	slog.Info("backup created", "id", id, "size", meta.Size, "checksum", meta.Checksum, "encrypted", encrypted)
	return &backupv1.CreateBackupResponse{Backup: toProto(meta)}, nil
}

func (m *Module) RestoreBackup(ctx context.Context, req *backupv1.RestoreBackupRequest) (*backupv1.RestoreBackupResponse, error) {
	backupID := req.GetBackupId()
	if backupID == "" {
		return nil, status.Error(codes.InvalidArgument, "backup_id is required")
	}

	m.mu.Lock()
	meta, ok := m.backups[backupID]
	dir := m.dir
	allowed := append([]string(nil), m.allowedRoots...)
	peers := make(map[string]contracts.Backupable, len(m.peers))
	for id, p := range m.peers {
		peers[id] = p
	}
	cryptor := m.cryptor
	m.mu.Unlock()

	target, err := validateTargetPath(req.GetTargetPath(), allowed)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}
	if !ok {
		return nil, status.Error(codes.NotFound, "backup not found")
	}

	path := archivePath(dir, meta.ID)
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, status.Error(codes.NotFound, "backup archive missing on disk")
		}
		return nil, status.Errorf(codes.Internal, "stat archive: %v", err)
	}

	sum, _, err := sha256File(path)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "checksum archive: %v", err)
	}
	if meta.Checksum != "" && sum != meta.Checksum {
		return nil, status.Error(codes.FailedPrecondition, "backup archive checksum mismatch")
	}

	workArchive, cleanup, err := decryptArchiveToTemp(ctx, cryptor, path)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "decrypt archive: %v", err)
	}
	defer cleanup()

	staging, err := os.MkdirTemp(filepath.Dir(target), ".backup-restore-*")
	if err != nil {
		return nil, status.Errorf(codes.Internal, "create staging dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(staging) }()

	files, moduleStates, err := extractTarGzToDir(workArchive, staging)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "extract backup: %v", err)
	}

	for id, data := range moduleStates {
		peer, ok := peers[id]
		if !ok {
			continue
		}
		if err := peer.ImportState(ctx, data); err != nil {
			return nil, status.Errorf(codes.Internal, "import %q: %v", id, err)
		}
	}

	if err := atomicRestoreTree(staging, target); err != nil {
		return nil, status.Errorf(codes.Internal, "apply restore: %v", err)
	}

	slog.Info("backup restored", "id", backupID, "target", target, "files", files)
	return &backupv1.RestoreBackupResponse{Status: "ok", FilesRestored: files}, nil
}

func (m *Module) ListBackups(ctx context.Context, req *backupv1.ListBackupsRequest) (*backupv1.ListBackupsResponse, error) {
	m.mu.Lock()
	list := make([]*backupv1.BackupInfo, 0, len(m.backups))
	for _, b := range m.backups {
		list = append(list, toProto(b))
	}
	m.mu.Unlock()
	sort.Slice(list, func(i, j int) bool {
		return list[i].TimestampUnix > list[j].TimestampUnix
	})
	return &backupv1.ListBackupsResponse{Backups: list}, nil
}

func (m *Module) DeleteBackup(ctx context.Context, req *backupv1.DeleteBackupRequest) (*backupv1.DeleteBackupResponse, error) {
	m.mu.Lock()
	meta, ok := m.backups[req.GetBackupId()]
	dir := m.dir
	if ok {
		delete(m.backups, req.GetBackupId())
	}
	m.mu.Unlock()
	if !ok {
		return nil, status.Error(codes.NotFound, "backup not found")
	}
	arch := archivePath(dir, meta.ID)
	if _, err := os.Stat(arch); err != nil {
		m.mu.Lock()
		m.backups[meta.ID] = meta
		m.mu.Unlock()
		if os.IsNotExist(err) {
			return nil, status.Error(codes.NotFound, "backup archive missing on disk")
		}
		return nil, status.Errorf(codes.Internal, "stat archive: %v", err)
	}
	if err := os.Remove(arch); err != nil {
		m.mu.Lock()
		m.backups[meta.ID] = meta
		m.mu.Unlock()
		return nil, status.Errorf(codes.Internal, "remove archive: %v", err)
	}
	if err := m.saveIndex(); err != nil {
		m.mu.Lock()
		m.backups[meta.ID] = meta
		m.mu.Unlock()
		return nil, status.Errorf(codes.Internal, "persist index: %v", err)
	}
	return &backupv1.DeleteBackupResponse{Status: "ok"}, nil
}

func toProto(b backupMeta) *backupv1.BackupInfo {
	return &backupv1.BackupInfo{
		Id:             b.ID,
		TimestampUnix:  b.Timestamp,
		SizeBytes:      b.Size,
		ModuleIds:      b.ModuleIDs,
		ChecksumSha256: b.Checksum,
	}
}
