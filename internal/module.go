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
	modulesdk "github.com/Muxcore-Media/core/sdk/go/module"
)

type backupMeta struct {
	ID        string   `json:"id"`
	Timestamp int64    `json:"timestamp"`
	Size      int64    `json:"size"`
	Checksum  string   `json:"checksum_sha256"`
	ModuleIDs []string `json:"module_ids"`
}

// BackupablePeer is a named Backupable used when exporting/importing module state.
type BackupablePeer struct {
	ID      string
	Backend contracts.Backupable
}

type Module struct {
	backupv1.UnimplementedBackupServiceServer
	mu       sync.Mutex
	dir      string
	sources  []string
	peers    map[string]contracts.Backupable
	backups  map[string]backupMeta
	grpcSrv  *grpc.Server
	grpcLis  net.Listener
	httpLis  net.Listener
	id       string
	grpcAddr string
	httpAddr string
}

type Config struct {
	ID         string
	Dir        string
	SourceDirs []string
	Peers      []BackupablePeer
	GRPCAddr   string
	HTTPAddr   string
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
	peers := make(map[string]contracts.Backupable, len(cfg.Peers))
	for _, p := range cfg.Peers {
		if p.ID == "" || p.Backend == nil {
			continue
		}
		peers[p.ID] = p.Backend
	}
	return &Module{
		id:       cfg.ID,
		dir:      cfg.Dir,
		sources:  append([]string(nil), cfg.SourceDirs...),
		peers:    peers,
		grpcAddr: cfg.GRPCAddr,
		httpAddr: cfg.HTTPAddr,
		backups:  make(map[string]backupMeta),
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
		Version:      "0.1.2",
		Roles:        []string{"infrastructure"},
		Description:  "Local filesystem backup/restore provider",
		Author:       "MuxCore",
		Capabilities: []string{contracts.CapabilityBackup, "backup.local", "settings"},
		HTTPAddr:     m.grpcAddr,
	}
}

func (m *Module) Init(ctx context.Context) error {
	if err := os.MkdirAll(m.dir, 0700); err != nil {
		return fmt.Errorf("create backup dir: %w", err)
	}
	if err := m.loadIndex(); err != nil {
		slog.Warn("could not load backup index", "error", err)
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
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	go func() {
		slog.Info("backup HTTP started", "addr", m.httpAddr)
		_ = http.Serve(m.httpLis, mux)
	}()
	return nil
}

func (m *Module) Stop(ctx context.Context) error {
	if m.grpcSrv != nil {
		m.grpcSrv.GracefulStop()
	}
	if m.httpLis != nil {
		_ = m.httpLis.Close()
	}
	slog.Info("backup-local stopped")
	return nil
}

func (m *Module) Health(ctx context.Context) error {
	return nil
}

func (m *Module) CreateBackup(ctx context.Context, req *backupv1.CreateBackupRequest) (*backupv1.CreateBackupResponse, error) {
	m.mu.Lock()
	sources := append([]string(nil), m.sources...)
	dir := m.dir
	peers := make(map[string]contracts.Backupable, len(m.peers))
	for id, p := range m.peers {
		peers[id] = p
	}
	m.mu.Unlock()
	sources = append(sources, req.GetSourcePaths()...)

	peerIDs := req.GetModuleIds()

	if len(peerIDs) == 0 {
		for id := range peers {
			peerIDs = append(peerIDs, id)
		}
		sort.Strings(peerIDs)
	}

	var entries []tarEntry
	usedPrefixes := make(map[string]int)
	for _, src := range sources {
		if strings.TrimSpace(src) == "" {
			continue
		}
		prefix := filepath.ToSlash(filepath.Join("data", uniqueSourcePrefix(src, usedPrefixes)))
		collected, err := collectDirEntries(src, prefix)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "source %q: %v", src, err)
		}
		entries = append(entries, collected...)
	}

	includedModules := make([]string, 0, len(peerIDs))
	for _, id := range peerIDs {
		peer, ok := peers[id]
		if !ok {
			return nil, status.Errorf(codes.NotFound, "backupable peer %q not registered", id)
		}
		data, err := peer.ExportState(ctx)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "export %q: %v", id, err)
		}
		entries = append(entries, tarEntry{
			Name: filepath.ToSlash(filepath.Join("modules", id, "state.bin")),
			Data: data,
			Mode: 0600,
		})
		includedModules = append(includedModules, id)
	}

	if len(entries) == 0 {
		return nil, status.Error(codes.FailedPrecondition, "nothing to back up: configure BACKUP_SOURCE_DIRS, pass source_paths, or register Backupable peers")
	}

	id := fmt.Sprintf("backup_%d", time.Now().UnixNano())
	path := archivePath(dir, id)
	if err := writeTarGz(path, entries); err != nil {
		_ = os.Remove(path)
		return nil, status.Errorf(codes.Internal, "write backup: %v", err)
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
	}

	m.mu.Lock()
	m.backups[id] = meta
	m.saveIndex()
	m.mu.Unlock()

	slog.Info("backup created", "id", id, "size", meta.Size, "checksum", meta.Checksum, "entries", len(entries))
	return &backupv1.CreateBackupResponse{Backup: toProto(meta)}, nil
}

func (m *Module) RestoreBackup(ctx context.Context, req *backupv1.RestoreBackupRequest) (*backupv1.RestoreBackupResponse, error) {
	backupID := req.GetBackupId()
	if backupID == "" {
		return nil, status.Error(codes.InvalidArgument, "backup_id is required")
	}
	target, err := validateTargetDir(req.GetTargetPath())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}

	m.mu.Lock()
	meta, ok := m.backups[backupID]
	dir := m.dir
	peers := make(map[string]contracts.Backupable, len(m.peers))
	for id, p := range m.peers {
		peers[id] = p
	}
	m.mu.Unlock()
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

	if err := os.MkdirAll(target, 0700); err != nil {
		return nil, status.Errorf(codes.Internal, "create target: %v", err)
	}

	files, moduleStates, err := extractTarGz(path, target)
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
		m.saveIndex()
	}
	m.mu.Unlock()
	if !ok {
		return nil, status.Error(codes.NotFound, "backup not found")
	}
	_ = os.Remove(archivePath(dir, meta.ID))
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

func (m *Module) loadIndex() error {
	data, err := os.ReadFile(filepath.Join(m.dir, "index.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var list []backupMeta
	if err := json.Unmarshal(data, &list); err != nil {
		return err
	}
	for _, b := range list {
		m.backups[b.ID] = b
	}
	return nil
}

func (m *Module) saveIndex() {
	list := make([]backupMeta, 0, len(m.backups))
	for _, b := range m.backups {
		list = append(list, b)
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		slog.Error("marshal backup index", "error", err)
		return
	}
	path := filepath.Join(m.dir, "index.json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		slog.Error("write backup index tmp", "error", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		slog.Error("rename backup index", "error", err)
	}
}
