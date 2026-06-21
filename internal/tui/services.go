// Package tui is the Bubble Tea terminal UI for fetch. It is presentation and
// async glue over the builder/engine/pipeline/store service APIs; it holds no
// domain logic except the pure promote helper.
package tui

import (
	"context"

	"github.com/cole/fetch/internal/builder"
	"github.com/cole/fetch/internal/config"
	"github.com/cole/fetch/internal/core"
	"github.com/cole/fetch/internal/engine"
	"github.com/cole/fetch/internal/pipeline"
	"github.com/cole/fetch/internal/providers/store"
)

// Session is the create-time interview API the Create screen drives.
// *builder.CreateSession satisfies it.
type Session interface {
	Start(goal string)
	Reply(ctx context.Context, msg string) (question string, ready bool, err error)
	Facts() builder.Facts
	Finalize(ctx context.Context) (builder.Draft, error)
	Redraft(ctx context.Context, comment string) (builder.Draft, error)
	Accept(ctx context.Context, d builder.Draft) (id string, err error)
}

// EngineRunner is the run API the Run screen drives. *engine.Engine satisfies it.
type EngineRunner interface {
	Run(ctx context.Context, p core.Pipeline, input map[string]any, events chan<- engine.Event) (engine.RunResult, error)
}

// Deps are the externally-built collaborators for the TUI.
type Deps struct {
	Cfg        config.Config
	Repo       *pipeline.Repository
	Store      store.Store
	NewSession func() Session      // fresh interview session per create
	NewEngine  func() EngineRunner // engine for runs
}

type services struct {
	cfg        config.Config
	repo       *pipeline.Repository
	store      store.Store
	newSession func() Session
	newEngine  func() EngineRunner
}

func (d Deps) services() services {
	return services{cfg: d.Cfg, repo: d.Repo, store: d.Store, newSession: d.NewSession, newEngine: d.NewEngine}
}

type screen int

const (
	screenHome screen = iota
	screenCreate
	screenRun
	screenResults
)

// navMsg asks the root model to switch screens, optionally carrying a pipeline.
type navMsg struct {
	to       screen
	pipeline core.Pipeline
}

// statusMsg sets the root status line.
type statusMsg struct {
	text string
	err  bool
}
