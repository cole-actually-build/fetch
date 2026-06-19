// Package engine executes pipeline plans: dependency-ordered step execution,
// run/trace recording, progress events, and bounded agent fallback.
package engine

import "github.com/cole/fetch/internal/core"

// EventType categorizes a progress event streamed during a run.
type EventType string

const (
	EventRunStarted   EventType = "run_started"
	EventStepStarted  EventType = "step_started"
	EventStepRetry    EventType = "step_retry"
	EventFallback     EventType = "fallback"
	EventStepFinished EventType = "step_finished"
	EventRunFinished  EventType = "run_finished"
)

// Event is a single progress update the engine emits over a channel.
type Event struct {
	RunID   string
	StepID  string
	Type    EventType
	Message string
	Status  string
}

// Fallback decision actions.
const (
	ActionAdapt = "adapt"
	ActionSkip  = "skip"
	ActionAbort = "abort"
)

// Decision is the Replanner's verdict on a failed step.
type Decision struct {
	Action string
	Step   core.Step // for ActionAdapt: the patched step
	Reason string
}

// ReplanRequest is the context handed to the Replanner on failure.
type ReplanRequest struct {
	Pipeline core.Pipeline
	Step     core.Step
	Params   map[string]any
	Attempt  int
	Err      string
}
