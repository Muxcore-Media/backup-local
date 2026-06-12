package internal

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

type Module struct {
	id       string
	meshAddr string
}

func NewModule() *Module {
	return &Module{
		id:       "your-module",
		meshAddr: "",
	}
}

func (m *Module) Info() contracts.ModuleInfo {
	return contracts.ModuleInfo{
		ID:           m.id,
		Name:         "Your Module",
		Version:      "0.1.0",
		Roles:        []string{},
		Description:  "Describe what your module does.",
		Author:       "You",
		Capabilities: []string{},
		DependsOn:    []string{},
	}
}

func (m *Module) Init(ctx context.Context) error {
	slog.Info("initializing module", "id", m.id)
	return nil
}

func (m *Module) Start(ctx context.Context) error {
	slog.Info("starting module", "id", m.id)
	return nil
}

func (m *Module) Stop(ctx context.Context) error {
	slog.Info("stopping module", "id", m.id)
	return nil
}

func (m *Module) Health(ctx context.Context) error {
	return fmt.Errorf("not implemented")
}
