package main

import (
	"log/slog"
	"os"

	modulesdk "github.com/Muxcore-Media/core/sdk/go/module"
	"github.com/yourorg/your-module/internal"
)

func main() {
	mod := internal.NewModule()
	if err := modulesdk.Run(modulesdk.Config{
		Module:   mod,
		Insecure: true,
	}); err != nil {
		slog.Error("module exited", "error", err)
		os.Exit(1)
	}
}
