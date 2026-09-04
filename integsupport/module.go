// Package integsupport exposes backup-local internals to umbrella integration tests.
package integsupport

import (
	"context"
	"testing"

	backup "github.com/Muxcore-Media/backup-local/internal"
)

// Module is the backup-local sidecar implementation.
type Module = backup.Module

// Config configures a backup-local test module.
type Config = backup.Config

// NewModule constructs a backup-local module.
func NewModule(cfg Config) *Module {
	return backup.NewModule(cfg)
}

// NewTestModule initializes a temp-dir backup module and registers cleanup.
func NewTestModule(t *testing.T, cfg Config) *Module {
	t.Helper()
	if cfg.Dir == "" {
		cfg.Dir = t.TempDir()
	}
	if cfg.GRPCAddr == "" {
		cfg.GRPCAddr = "127.0.0.1:0"
	}
	if cfg.HTTPAddr == "" {
		cfg.HTTPAddr = "127.0.0.1:0"
	}
	if cfg.MaxBackups == 0 {
		cfg.MaxBackups = 10
	}
	m := backup.NewModule(cfg)
	ctx := context.Background()
	if err := m.Init(ctx); err != nil {
		t.Fatalf("backup-local init: %v", err)
	}
	t.Cleanup(func() { _ = m.Stop(ctx) })
	return m
}

// Start connects the module to core when MUXCORE_GRPC_ADDR is set.
func Start(ctx context.Context, m *Module) error {
	return m.Start(ctx)
}

// GRPCListenAddr returns the bound gRPC address after Init.
func GRPCListenAddr(m *Module) string {
	return backup.IntegrationListenAddr(m)
}
