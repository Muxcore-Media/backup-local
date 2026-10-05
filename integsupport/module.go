// Package integsupport exposes backup-local internals to umbrella integration tests.
package integsupport

import (
	"context"
	"testing"

	backup "github.com/Muxcore-Media/backup-local/internal"
	"github.com/Muxcore-Media/core/pkg/contracts"
)

// Module is the backup-local sidecar implementation.
type Module = backup.Module

// Config configures a backup-local test module.
type Config = backup.Config

// BackupablePeer is a named Backupable used when exporting state.
type BackupablePeer = backup.BackupablePeer

// DefaultPeerID is the Backupable peer registered by NewTestModule when the
// config has no peers and no source directories, so CreateBackup has content.
const DefaultPeerID = "media-movies"

type staticPeer struct{ state []byte }

func (p staticPeer) ExportState(context.Context) ([]byte, error) { return p.state, nil }
func (p staticPeer) ImportState(context.Context, []byte) error   { return nil }

var _ contracts.Backupable = staticPeer{}

// NewModule constructs a backup-local module.
func NewModule(cfg Config) *Module {
	return backup.NewModule(cfg)
}

// NewTestModule initializes a temp-dir backup module on loopback addresses and
// registers cleanup. Environment overrides (BACKUP_DIR etc.) still apply as in
// production, so tests should not set them.
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
	if len(cfg.Peers) == 0 && len(cfg.SourceDirs) == 0 {
		cfg.Peers = []BackupablePeer{{ID: DefaultPeerID, Backend: staticPeer{state: []byte("integsupport-state")}}}
	}
	m := backup.NewModule(cfg)
	ctx := context.Background()
	if err := m.Init(ctx); err != nil {
		t.Fatalf("backup-local init: %v", err)
	}
	t.Cleanup(func() { _ = m.Stop(ctx) })
	return m
}

// Start starts the module's servers (and scheduler, if configured).
func Start(ctx context.Context, m *Module) error {
	return m.Start(ctx)
}
