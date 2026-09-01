package main

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	modulesdk "github.com/Muxcore-Media/core/sdk/go/module"

	"github.com/Muxcore-Media/backup-local/internal"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--health-check" {
		os.Exit(runHealthCheck())
	}
	mod := internal.NewModule(internal.Config{})
	insecure := os.Getenv("MUXCORE_INSECURE_DISABLE_TLS") == "true" || os.Getenv("MUXCORE_GRPC_INSECURE") == "true"
	if err := modulesdk.Run(modulesdk.Config{
		Module:   mod,
		Insecure: insecure,
	}); err != nil {
		slog.Error("module exited", "error", err)
		os.Exit(1)
	}
}

func runHealthCheck() int {
	addr := os.Getenv("BACKUP_HTTP_ADDR")
	if addr == "" {
		addr = ":9303"
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		if strings.HasPrefix(addr, ":") {
			host, port = "127.0.0.1", strings.TrimPrefix(addr, ":")
		} else {
			fmt.Fprintf(os.Stderr, "health-check: bad BACKUP_HTTP_ADDR: %v\n", err)
			return 1
		}
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	url := fmt.Sprintf("http://%s/health", net.JoinHostPort(host, port))
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "health-check: %v\n", err)
		return 1
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "health-check: status %d\n", resp.StatusCode)
		return 1
	}
	return 0
}
