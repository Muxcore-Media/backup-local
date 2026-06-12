package internal

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	backupv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/backup/v1"
	"github.com/Muxcore-Media/core/pkg/contracts"
)

type backupMeta struct {
	ID        string   `json:"id"`
	Timestamp int64    `json:"timestamp"`
	Size      int64    `json:"size"`
	ModuleIDs []string `json:"module_ids"`
}

type Module struct {
	backupv1.UnimplementedBackupServiceServer
	mu       sync.Mutex
	dir      string
	backups  map[string]backupMeta
	grpcSrv  *grpc.Server
	grpcLis  net.Listener
	httpLis  net.Listener
	id       string
	grpcAddr string
	httpAddr string
}

type Config struct {
	ID       string
	Dir      string
	GRPCAddr string
	HTTPAddr string
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
	return &Module{
		id:       cfg.ID,
		dir:      cfg.Dir,
		grpcAddr: cfg.GRPCAddr,
		httpAddr: cfg.HTTPAddr,
		backups:  make(map[string]backupMeta),
	}
}

func (m *Module) Info() contracts.ModuleInfo {
	return contracts.ModuleInfo{
		ID:           m.id,
		Name:         "Backup Local",
		Version:      "0.1.0",
		Roles:        []string{"infrastructure"},
		Description:  "Local filesystem backup/restore provider",
		Author:       "MuxCore",
		Capabilities: []string{contracts.CapabilityBackup},
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
	go func() {
		slog.Info("backup gRPC started", "addr", m.grpcAddr)
		if err := m.grpcSrv.Serve(m.grpcLis); err != nil {
			slog.Error("backup gRPC error", "error", err)
		}
	}()
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})
	go func() {
		slog.Info("backup HTTP started", "addr", m.httpAddr)
		http.Serve(m.httpLis, mux)
	}()
	return nil
}

func (m *Module) Stop(ctx context.Context) error {
	if m.grpcSrv != nil {
		m.grpcSrv.GracefulStop()
	}
	slog.Info("backup-local stopped")
	return nil
}

func (m *Module) Health(ctx context.Context) error {
	return nil
}

func (m *Module) CreateBackup(ctx context.Context, req *backupv1.CreateBackupRequest) (*backupv1.CreateBackupResponse, error) {
	id := fmt.Sprintf("backup_%d", time.Now().Unix())
	path := filepath.Join(m.dir, id+".tar.gz")

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	tw.Flush()
	gw.Close()
	tw.Close()

	if err := os.WriteFile(path, buf.Bytes(), 0600); err != nil {
		return nil, status.Error(codes.Internal, "write backup failed")
	}

	meta := backupMeta{
		ID:        id,
		Timestamp: time.Now().Unix(),
		Size:      int64(buf.Len()),
		ModuleIDs: req.GetModuleIds(),
	}

	m.mu.Lock()
	m.backups[id] = meta
	m.saveIndex()
	m.mu.Unlock()

	slog.Info("backup created", "id", id, "size", meta.Size)
	return &backupv1.CreateBackupResponse{
		Backup: &backupv1.BackupInfo{
			Id: id, TimestampUnix: meta.Timestamp,
			SizeBytes: meta.Size, ModuleIds: meta.ModuleIDs,
		},
	}, nil
}

func (m *Module) RestoreBackup(ctx context.Context, req *backupv1.RestoreBackupRequest) (*backupv1.RestoreBackupResponse, error) {
	m.mu.Lock()
	meta, ok := m.backups[req.GetBackupId()]
	m.mu.Unlock()
	if !ok {
		return nil, status.Error(codes.NotFound, "backup not found")
	}
	_ = meta
	return &backupv1.RestoreBackupResponse{Status: "ok"}, nil
}

func (m *Module) ListBackups(ctx context.Context, req *backupv1.ListBackupsRequest) (*backupv1.ListBackupsResponse, error) {
	m.mu.Lock()
	var list []*backupv1.BackupInfo
	for _, b := range m.backups {
		list = append(list, &backupv1.BackupInfo{
			Id: b.ID, TimestampUnix: b.Timestamp,
			SizeBytes: b.Size, ModuleIds: b.ModuleIDs,
		})
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
	if ok {
		delete(m.backups, req.GetBackupId())
		m.saveIndex()
	}
	m.mu.Unlock()
	if !ok {
		return nil, status.Error(codes.NotFound, "backup not found")
	}
	os.Remove(filepath.Join(m.dir, meta.ID+".tar.gz"))
	return &backupv1.DeleteBackupResponse{Status: "ok"}, nil
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
	os.Rename(tmp, path)
}
