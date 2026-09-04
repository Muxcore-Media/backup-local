package internal

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	backupv1 "github.com/Muxcore-Media/backup-local/muxcore/backup/v1"
	"github.com/Muxcore-Media/core/pkg/contracts"
	encryptionv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/encryption/v1"
	"github.com/Muxcore-Media/core/sdk/go/client"
)

type grpcBackupable struct {
	id     string
	client backupv1.BackupableServiceClient
	conn   *grpc.ClientConn
}

func (g *grpcBackupable) ExportState(ctx context.Context) ([]byte, error) {
	resp, err := g.client.ExportState(ctx, &backupv1.ExportStateRequest{})
	if err != nil {
		return nil, err
	}
	return resp.GetData(), nil
}

func (g *grpcBackupable) ImportState(ctx context.Context, data []byte) error {
	_, err := g.client.ImportState(ctx, &backupv1.ImportStateRequest{Data: data})
	return err
}

func (g *grpcBackupable) Close() error {
	if g.conn != nil {
		return g.conn.Close()
	}
	return nil
}

func dialPeerBackupable(moduleID, addr string, _ bool) (*grpcBackupable, error) {
	addr = dialAddrForPeer(moduleID, addr)
	if addr == "" {
		return nil, fmt.Errorf("empty dial address for module %q", moduleID)
	}
	// Sidecar service ports speak plaintext gRPC on loopback/LAN.
	opts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	conn, err := grpc.NewClient(addr, opts...)
	if err != nil {
		return nil, fmt.Errorf("dial %q: %w", addr, err)
	}
	return &grpcBackupable{
		id:     moduleID,
		client: backupv1.NewBackupableServiceClient(conn),
		conn:   conn,
	}, nil
}

func dialAddrForPeer(moduleID, httpAddr string) string {
	httpAddr = strings.TrimSpace(httpAddr)
	if httpAddr == "" {
		return ""
	}
	host, port, err := net.SplitHostPort(httpAddr)
	if err != nil || port == "" {
		return httpAddr
	}
	if host != "" && host != "0.0.0.0" && host != "::" {
		return net.JoinHostPort(host, port)
	}
	if os.Getenv("MUXCORE_MESH_DIAL_LOCAL") == "true" {
		return net.JoinHostPort("127.0.0.1", port)
	}
	if moduleID != "" {
		return net.JoinHostPort(moduleID, port)
	}
	return httpAddr
}

func (m *Module) refreshBackupablePeers(ctx context.Context) {
	mc := m.meshClient()
	if mc == nil {
		return
	}
	modules, err := mc.Discovery.FindByCapability(ctx, "backupable")
	if err != nil {
		slog.Debug("backup-local: FindByCapability backupable failed", "error", err)
		return
	}
	insecureTLS := os.Getenv("MUXCORE_INSECURE_DISABLE_TLS") == "true" || os.Getenv("MUXCORE_GRPC_INSECURE") == "true"
	peers := make(map[string]contracts.Backupable)
	conns := make([]*grpcBackupable, 0)
	for _, info := range modules {
		id := info.GetId()
		if id == "" || id == m.id {
			continue
		}
		g, err := dialPeerBackupable(id, info.GetHttpAddr(), insecureTLS)
		if err != nil {
			slog.Warn("backup-local: dial backupable peer failed", "module", id, "error", err)
			continue
		}
		peers[id] = g
		conns = append(conns, g)
	}
	m.mu.Lock()
	for _, old := range m.peerConns {
		if err := old.Close(); err != nil {
			slog.Warn("backup-local: close stale peer connection failed", "error", err)
		}
	}
	m.peers = peers
	m.peerConns = conns
	m.mu.Unlock()
	if len(peers) > 0 {
		slog.Info("backup-local: registered backupable peers", "count", len(peers))
	}
}

func (m *Module) meshClient() *client.Client {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mc
}

func (m *Module) dialCoreLoop(ctx context.Context) {
	meshAddr := strings.TrimSpace(os.Getenv("MUXCORE_GRPC_ADDR"))
	if meshAddr == "" {
		return
	}
	delay := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		if m.tryDialCore(meshAddr) {
			slog.Info("backup-local: connected to core mesh", "addr", meshAddr)
			m.refreshBackupablePeers(ctx)
			go m.peerRefreshLoop(ctx)
			return
		}
		if err := sleepContext(ctx, delay); err != nil {
			return
		}
		if delay < 30*time.Second {
			delay *= 2
		}
	}
}

func (m *Module) tryDialCore(meshAddr string) bool {
	var opts []client.Option
	if os.Getenv("MUXCORE_INSECURE_DISABLE_TLS") == "true" || os.Getenv("MUXCORE_GRPC_INSECURE") == "true" {
		opts = append(opts, client.WithInsecure())
	}
	c, err := client.Dial(meshAddr, opts...)
	if err != nil {
		slog.Warn("backup-local: dial core failed", "error", err)
		return false
	}
	m.mu.Lock()
	if m.mc != nil {
		if err := m.mc.Close(); err != nil {
			slog.Warn("backup-local: close previous mesh client failed", "error", err)
		}
	}
	m.mc = c
	if m.cryptor == nil {
		cryptor, encConn := m.buildCryptor(c)
		if encConn != nil {
			m.encConn = encConn
		}
		if cryptor != nil {
			m.cryptor = cryptor
		}
	}
	m.mu.Unlock()
	return true
}

func (m *Module) peerRefreshLoop(ctx context.Context) {
	t := time.NewTicker(2 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.refreshBackupablePeers(ctx)
		}
	}
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (m *Module) buildCryptor(mc *client.Client) (*archiveCryptor, *grpc.ClientConn) {
	key := os.Getenv("BACKUP_ENCRYPT_KEY")
	if strings.TrimSpace(key) != "" {
		c, err := newArchiveCryptor(key, nil)
		if err != nil {
			slog.Warn("backup-local: local encryption init failed", "error", err)
			return nil, nil
		}
		return c, nil
	}
	if mc == nil {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	modules, err := mc.Discovery.FindByCapability(ctx, "encryption")
	if err != nil || len(modules) == 0 {
		return nil, nil
	}
	info := modules[0]
	addr := dialAddrForPeer(info.GetId(), info.GetHttpAddr())
	if addr == "" {
		return nil, nil
	}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	conn, err := grpc.NewClient(addr, opts...)
	if err != nil {
		slog.Warn("backup-local: dial encryption module failed", "error", err)
		return nil, nil
	}
	client := encryptionv1.NewEncryptionServiceClient(conn)
	c, err := newArchiveCryptor("", client)
	if err != nil {
		slog.Warn("backup-local: encryption module cryptor init failed", "error", err)
		_ = conn.Close()
		return nil, nil
	}
	slog.Info("backup-local: using encryption-aesgcm for archives", "module", info.GetId())
	return c, conn
}
