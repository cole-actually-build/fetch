// Package builder turns a natural-language goal into a saved core.Pipeline
// through an agentic interview, schema design, and planning with a bounded
// validate-repair loop. Each agent role is one structured LLM call.
package builder

import "github.com/cole/fetch/internal/core"

// Turn is one message in the interview transcript ("user" or "assistant").
type Turn struct {
	Role    string
	Content string
}

// Facts is the running structured understanding the Interviewer accumulates.
type Facts struct {
	Domain       string            `json:"domain"`
	Inputs       []core.InputParam `json:"inputs"`
	OutputFields []core.Field      `json:"output_fields"`
	SourceHints  []string          `json:"source_hints"`
}

// InterviewState is the input to one Interviewer turn.
type InterviewState struct {
	Goal       string
	Transcript []Turn
	Facts      Facts
	Done       bool
}

// Draft is a reviewable proposed pipeline.
type Draft struct {
	Pipeline core.Pipeline
	Notes    string
}
