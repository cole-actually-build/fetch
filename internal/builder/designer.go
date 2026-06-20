package builder

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cole/fetch/internal/agent"
	"github.com/cole/fetch/internal/config"
	"github.com/cole/fetch/internal/core"
)

type designOutput struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Domain      string            `json:"domain"`
	Inputs      []core.InputParam `json:"inputs"`
	Schema      []core.Field      `json:"schema"`
}

// SchemaDesigner turns the interview facts into pipeline inputs + output schema.
type SchemaDesigner struct {
	llm agent.LLM
	cfg config.Config
}

func NewSchemaDesigner(llm agent.LLM, cfg config.Config) *SchemaDesigner {
	return &SchemaDesigner{llm: llm, cfg: cfg}
}

func schemaOutputSchema() json.RawMessage {
	return rawSchema(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":        map[string]any{"type": "string"},
			"description": map[string]any{"type": "string"},
			"domain":      map[string]any{"type": "string"},
			"inputs":      map[string]any{"type": "array", "items": inputParamSchema()},
			"schema":      map[string]any{"type": "array", "items": fieldSchema()},
		},
		"required": []string{"name", "description", "domain", "inputs", "schema"},
	})
}

func schemaSystemPrompt() string {
	return "You design the inputs and output schema for a web research pipeline. " +
		"Given the goal and gathered facts, return a short name, a one-line description, a domain tag, " +
		"the run inputs (values the user provides per run), and the output schema (the columns of each " +
		"result row). Keep the schema minimal but complete. Types must be one of: " +
		"string, int, float, bool, timestamp."
}

func (d *SchemaDesigner) Design(ctx context.Context, goal string, facts Facts, feedback string) (designOutput, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "Goal: %s\n\nFacts: %s\n", goal, factsJSON(facts))
	if feedback != "" {
		fmt.Fprintf(&b, "\n%s\n", feedback)
	}
	resp, err := d.llm.Chat(ctx, agent.ChatRequest{
		Model: d.cfg.ModelFor(config.RoleSchema),
		Messages: []agent.Message{
			{Role: "system", Content: schemaSystemPrompt()},
			{Role: "user", Content: b.String()},
		},
		Format: schemaOutputSchema(),
	})
	if err != nil {
		return designOutput{}, err
	}
	var out designOutput
	if err := json.Unmarshal([]byte(resp.Content), &out); err != nil {
		return designOutput{}, fmt.Errorf("designer: decode: %w", err)
	}
	return out, nil
}
