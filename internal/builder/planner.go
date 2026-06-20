package builder

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cole/fetch/internal/agent"
	"github.com/cole/fetch/internal/config"
	"github.com/cole/fetch/internal/core"
	"github.com/cole/fetch/internal/pipeline"
)

// Planner turns a finalized schema into an ordered, valid step plan.
type Planner struct {
	llm agent.LLM
	cfg config.Config
}

func NewPlanner(llm agent.LLM, cfg config.Config) *Planner {
	return &Planner{llm: llm, cfg: cfg}
}

func stepSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":         map[string]any{"type": "string"},
			"name":       map[string]any{"type": "string"},
			"type":       map[string]any{"type": "string", "enum": []string{"search", "fetch", "extract", "transform", "store"}},
			"params":     map[string]any{"type": "object"},
			"depends_on": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
		"required": []string{"id", "name", "type", "params"},
	}
}

func planOutputSchema() json.RawMessage {
	return rawSchema(map[string]any{
		"type":       "object",
		"properties": map[string]any{"plan": map[string]any{"type": "array", "items": stepSchema()}},
		"required":   []string{"plan"},
	})
}

func planSystemPrompt(base core.Pipeline) string {
	return "You design the ordered execution plan for a web research pipeline as a list of steps.\n" +
		"Allowed step types and their contract:\n" +
		"- search: params {query string, max_results int?}; produces urls and results.\n" +
		"- fetch: params {urls}; reference a search step via \"{{steps.<id>.urls}}\"; produces pages.\n" +
		"- extract: params {pages}; reference a fetch step via \"{{steps.<id>.pages}}\"; produces rows matching the schema.\n" +
		"- transform: params {rows, op: dedup|limit, by?, n?}.\n" +
		"- store: params {rows}; persists the rows.\n" +
		"Reference run inputs as \"{{input.<name>}}\". Each step needs a unique id and a depends_on " +
		"list naming the ids it references. A typical plan is search -> fetch -> extract -> store.\n" +
		"Output fields to produce: " + fieldNames(base.Schema) + ".\n" +
		"Inputs available: " + inputNames(base.Inputs) + "."
}

func planUserPrompt(base core.Pipeline, facts Facts, feedback string) string {
	schemaJSON, _ := json.Marshal(base.Schema)
	inputsJSON, _ := json.Marshal(base.Inputs)
	var b strings.Builder
	fmt.Fprintf(&b, "Pipeline: %s\nOutput schema: %s\nInputs: %s\nFacts: %s\n",
		base.Name, schemaJSON, inputsJSON, factsJSON(facts))
	if feedback != "" {
		fmt.Fprintf(&b, "\n%s\n", feedback)
	}
	b.WriteString("\nReturn the plan.")
	return b.String()
}

// Plan runs a single planning call and returns the proposed steps.
func (p *Planner) Plan(ctx context.Context, base core.Pipeline, facts Facts, feedback string) ([]core.Step, error) {
	resp, err := p.llm.Chat(ctx, agent.ChatRequest{
		Model: p.cfg.ModelFor(config.RolePlan),
		Messages: []agent.Message{
			{Role: "system", Content: planSystemPrompt(base)},
			{Role: "user", Content: planUserPrompt(base, facts, feedback)},
		},
		Format: planOutputSchema(),
	})
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Plan []core.Step `json:"plan"`
	}
	if err := json.Unmarshal([]byte(resp.Content), &parsed); err != nil {
		return nil, fmt.Errorf("planner: decode: %w", err)
	}
	return parsed.Plan, nil
}

// PlanWithRepair fills base.Plan, validates it, and on failure re-invokes the
// Planner with the validation error appended, up to maxRepairs times.
func (p *Planner) PlanWithRepair(ctx context.Context, base core.Pipeline, facts Facts, feedback string, maxRepairs int) (core.Pipeline, error) {
	fb := feedback
	var lastErr error
	for attempt := 0; attempt <= maxRepairs; attempt++ {
		steps, err := p.Plan(ctx, base, facts, fb)
		if err != nil {
			return core.Pipeline{}, err
		}
		cand := base
		cand.Plan = steps
		if err := pipeline.Validate(cand); err == nil {
			return cand, nil
		} else {
			lastErr = err
			prev, _ := json.Marshal(steps)
			fb = fmt.Sprintf("%s\n\nYour previous plan failed validation: %v\nPrevious plan JSON: %s\nReturn a corrected plan.",
				feedback, err, prev)
		}
	}
	return core.Pipeline{}, fmt.Errorf("planner: plan still invalid after %d repairs: %w", maxRepairs, lastErr)
}
