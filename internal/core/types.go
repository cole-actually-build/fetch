// Package core holds fetch's pure domain types. It has no dependencies on
// other internal packages so every layer can import it without cycles.
package core

import "time"

// FieldType is the storage type of an output field or input parameter.
type FieldType string

const (
	FieldString    FieldType = "string"
	FieldInt       FieldType = "int"
	FieldFloat     FieldType = "float"
	FieldBool      FieldType = "bool"
	FieldTimestamp FieldType = "timestamp"
)

// Field is one column of a pipeline's output schema.
type Field struct {
	Name        string    `json:"name"`
	Type        FieldType `json:"type"`
	Description string    `json:"description"`
}

// InputParam is a value the user supplies to start a run.
type InputParam struct {
	Name        string    `json:"name"`
	Type        FieldType `json:"type"`
	Required    bool      `json:"required"`
	Description string    `json:"description"`
}

// StepType enumerates the executor kinds.
type StepType string

const (
	StepSearch    StepType = "search"
	StepFetch     StepType = "fetch"
	StepExtract   StepType = "extract"
	StepTransform StepType = "transform"
	StepStore     StepType = "store"
)

// Step is one unit of work in a pipeline plan.
type Step struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Type      StepType       `json:"type"`
	Params    map[string]any `json:"params"`
	DependsOn []string       `json:"depends_on,omitempty"`
}

// Pipeline is the durable, agent-generated definition.
type Pipeline struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Domain      string            `json:"domain"`
	Inputs      []InputParam      `json:"inputs"`
	Schema      []Field           `json:"schema"`
	Plan        []Step            `json:"plan"`
	Models      map[string]string `json:"models,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	Version     int               `json:"version"`
}

// RunStatus is the lifecycle state of a single execution.
type RunStatus string

const (
	RunRunning RunStatus = "running"
	RunOK      RunStatus = "ok"
	RunFailed  RunStatus = "failed"
	RunPartial RunStatus = "partial"
)

// Run records one execution of a pipeline.
type Run struct {
	ID         string         `json:"id"`
	PipelineID string         `json:"pipeline_id"`
	Input      map[string]any `json:"input"`
	Status     RunStatus      `json:"status"`
	StartedAt  time.Time      `json:"started_at"`
	FinishedAt time.Time      `json:"finished_at"`
}

// StepTrace records what happened to one step within a run.
type StepTrace struct {
	RunID         string   `json:"run_id"`
	StepID        string   `json:"step_id"`
	Status        string   `json:"status"`
	InputSummary  string   `json:"input_summary"`
	OutputSummary string   `json:"output_summary"`
	ArtifactRefs  []string `json:"artifact_refs"`
	Tokens        int      `json:"tokens"`
	Error         string   `json:"error"`
	FallbackUsed  bool     `json:"fallback_used"`
}
