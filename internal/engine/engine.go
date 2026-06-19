package engine

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cole/fetch/internal/agent"
	"github.com/cole/fetch/internal/config"
	"github.com/cole/fetch/internal/core"
	"github.com/cole/fetch/internal/pipeline"
	"github.com/cole/fetch/internal/providers/artifacts"
	"github.com/cole/fetch/internal/providers/fetch"
	"github.com/cole/fetch/internal/providers/search"
	"github.com/cole/fetch/internal/providers/store"
)

// Replanner decides how to recover from a failed step (nil ⇒ no fallback).
type Replanner interface {
	Replan(ctx context.Context, req ReplanRequest) (Decision, error)
}

// Revision is a self-heal candidate: a step that only succeeded after a
// run-time adaptation, offered for promotion into the pipeline definition.
type Revision struct {
	StepID   string
	Original core.Step
	Adapted  core.Step
	RunID    string
}

// RunResult is the outcome of a single Run.
type RunResult struct {
	Run        core.Run
	Traces     []core.StepTrace
	Candidates []Revision
}

// Deps are the engine's collaborators and tunables.
type Deps struct {
	Config      config.Config
	LLM         agent.LLM
	Search      search.Search
	Fetcher     fetch.Fetcher
	Artifacts   artifacts.Store
	Store       store.Store
	Replanner   Replanner            // optional
	Repo        *pipeline.Repository // optional, required for AutoPromote
	MaxRetries  int                  // default 2
	AutoPromote bool
	Now         func() time.Time // optional
	IDGen       func() string    // optional
}

// Engine executes pipelines.
type Engine struct {
	d Deps
}

// New builds an Engine, filling in defaults for unset tunables.
func New(d Deps) *Engine {
	if d.MaxRetries == 0 {
		d.MaxRetries = 2
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.IDGen == nil {
		d.IDGen = defaultIDGen()
	}
	return &Engine{d: d}
}

func defaultIDGen() func() string {
	var mu sync.Mutex
	var n int
	return func() string {
		mu.Lock()
		n++
		id := n
		mu.Unlock()
		return fmt.Sprintf("run-%d-%d", time.Now().UnixNano(), id)
	}
}

type runState struct {
	pipeline core.Pipeline
	run      *core.Run
	scope    pipeline.Scope
	events   chan<- Event
}

func (e *Engine) emit(rs *runState, ev Event) {
	ev.RunID = rs.run.ID
	if rs.events != nil {
		rs.events <- ev
	}
}

// Run executes a validated pipeline against input, recording runs/traces and
// streaming events (events may be nil). It returns once the plan finishes or a
// step aborts.
func (e *Engine) Run(ctx context.Context, p core.Pipeline, input map[string]any, events chan<- Event) (RunResult, error) {
	if err := pipeline.Validate(p); err != nil {
		return RunResult{}, err
	}
	order, err := pipeline.TopoOrder(p)
	if err != nil {
		return RunResult{}, err
	}
	run := core.Run{
		ID:         e.d.IDGen(),
		PipelineID: p.ID,
		Input:      input,
		Status:     core.RunRunning,
		StartedAt:  e.d.Now(),
	}
	rs := &runState{
		pipeline: p,
		run:      &run,
		scope:    pipeline.Scope{Input: input, Steps: map[string]map[string]any{}},
		events:   events,
	}
	_ = e.d.Store.RecordRun(ctx, run)
	e.emit(rs, Event{Type: EventRunStarted})

	var (
		traces     []core.StepTrace
		candidates []Revision
		runErr     error
	)
	status := core.RunOK
	for _, step := range order {
		e.emit(rs, Event{Type: EventStepStarted, StepID: step.ID, Message: step.Name})
		trace, revs, err := e.execStep(ctx, rs, step)
		traces = append(traces, trace)
		candidates = append(candidates, revs...)
		_ = e.d.Store.RecordTrace(ctx, trace)
		e.emit(rs, Event{Type: EventStepFinished, StepID: step.ID, Status: trace.Status})
		if err != nil {
			status = core.RunFailed
			runErr = err
			break
		}
		if trace.Status == "partial" && status == core.RunOK {
			status = core.RunPartial
		}
	}

	run.Status = status
	run.FinishedAt = e.d.Now()
	_ = e.d.Store.RecordRun(ctx, run)
	e.emit(rs, Event{Type: EventRunFinished, Status: string(status)})

	if e.d.AutoPromote && e.d.Repo != nil && len(candidates) > 0 {
		promoted := applyRevisions(p, candidates)
		promoted.Version++
		_ = e.d.Repo.Save(promoted)
	}

	return RunResult{Run: run, Traces: traces, Candidates: candidates}, runErr
}

// execStep runs one step, applying bounded fallback on failure. It returns the
// step's trace, any self-heal candidate revisions, and a terminal error (only
// when the step ultimately fails/aborts).
func (e *Engine) execStep(ctx context.Context, rs *runState, step core.Step) (core.StepTrace, []Revision, error) {
	trace := core.StepTrace{RunID: rs.run.ID, StepID: step.ID}
	current := step
	adapted := false

	for attempt := 0; ; attempt++ {
		params, rerr := pipeline.Resolve(current.Params, rs.scope)
		var (
			res stepResult
			err error
		)
		if rerr != nil {
			err = rerr
		} else {
			res, err = e.rawExec(ctx, rs, current, params)
		}
		if err == nil {
			rs.scope.Steps[step.ID] = res.output
			trace.Status = "ok"
			trace.Error = "" // clear any error recorded on a prior (self-healed) attempt
			trace.Tokens += res.tokens
			trace.ArtifactRefs = res.artifactRefs
			trace.OutputSummary = res.summary
			trace.FallbackUsed = adapted
			var revs []Revision
			if adapted {
				revs = append(revs, Revision{StepID: step.ID, Original: step, Adapted: current, RunID: rs.run.ID})
			}
			return trace, revs, nil
		}

		trace.Error = err.Error()
		e.emit(rs, Event{Type: EventStepRetry, StepID: step.ID, Message: err.Error()})

		if e.d.Replanner == nil || attempt >= e.d.MaxRetries {
			trace.Status = "failed"
			return trace, nil, err
		}
		decision, derr := e.d.Replanner.Replan(ctx, ReplanRequest{
			Pipeline: rs.pipeline, Step: current, Params: params, Attempt: attempt + 1, Err: err.Error(),
		})
		if derr != nil {
			trace.Status = "failed"
			return trace, nil, err
		}
		trace.FallbackUsed = true
		e.emit(rs, Event{Type: EventFallback, StepID: step.ID, Message: decision.Action + ": " + decision.Reason})
		switch decision.Action {
		case ActionAdapt:
			current = decision.Step
			adapted = true
			continue
		case ActionSkip:
			trace.Status = "partial"
			return trace, nil, nil
		default: // ActionAbort or unknown
			trace.Status = "failed"
			return trace, nil, err
		}
	}
}
