package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cole/fetch/internal/agent"
	"github.com/cole/fetch/internal/config"
	"github.com/cole/fetch/internal/core"
	"github.com/cole/fetch/internal/providers/fetch"
	"github.com/cole/fetch/internal/providers/search"
)

const extractMaxChars = 12000

func (e *Engine) execExtract(ctx context.Context, rs *runState, step core.Step, params map[string]any) (stepResult, error) {
	text := gatherExtractText(params)
	if strings.TrimSpace(text) == "" {
		return stepResult{}, fmt.Errorf("extract: no source text")
	}
	schema := rs.pipeline.Schema
	if len(schema) == 0 {
		return stepResult{}, fmt.Errorf("extract: pipeline has no schema")
	}
	format := buildExtractSchema(schema)
	sys := extractSystemPrompt(schema)
	user := "Source content:\n\n" + truncate(text, extractMaxChars)
	if instr, ok := params["instructions"].(string); ok && instr != "" {
		user = instr + "\n\n" + user
	}
	resp, err := e.d.LLM.Chat(ctx, agent.ChatRequest{
		Model: e.d.Config.ModelFor(config.RoleExtract),
		Messages: []agent.Message{
			{Role: "system", Content: sys},
			{Role: "user", Content: user},
		},
		Format: format,
	})
	if err != nil {
		return stepResult{}, err
	}
	rows, err := parseRows(resp.Content, schema)
	if err != nil {
		return stepResult{}, fmt.Errorf("extract: %w", err)
	}
	if len(rows) == 0 {
		return stepResult{}, fmt.Errorf("extract: no rows produced")
	}
	return stepResult{
		output:  map[string]any{"rows": rows},
		tokens:  resp.PromptTokens + resp.CompletionTokens,
		summary: fmt.Sprintf("extracted %d rows", len(rows)),
	}, nil
}

// gatherExtractText pulls source text from a "pages", "results", or "text" param.
func gatherExtractText(params map[string]any) string {
	if pages, ok := params["pages"].([]fetch.Page); ok {
		var b strings.Builder
		for _, p := range pages {
			b.WriteString(p.Text)
			b.WriteString("\n\n---\n\n")
		}
		return b.String()
	}
	if results, ok := params["results"].([]search.Result); ok {
		var b strings.Builder
		for _, r := range results {
			b.WriteString(r.Content)
			b.WriteString("\n\n---\n\n")
		}
		return b.String()
	}
	if s, ok := params["text"].(string); ok {
		return s
	}
	return ""
}

func extractSystemPrompt(schema []core.Field) string {
	var b strings.Builder
	b.WriteString("You extract structured records from web content. ")
	b.WriteString("Return JSON matching the provided schema: an object with a \"rows\" array. ")
	b.WriteString("Each row has these fields:\n")
	for _, f := range schema {
		b.WriteString(fmt.Sprintf("- %s (%s): %s\n", f.Name, f.Type, f.Description))
	}
	b.WriteString("Only include records actually supported by the content. If none, return an empty rows array.")
	return b.String()
}

func jsonType(t core.FieldType) string {
	switch t {
	case core.FieldInt:
		return "integer"
	case core.FieldFloat:
		return "number"
	case core.FieldBool:
		return "boolean"
	default: // string, timestamp
		return "string"
	}
}

// buildExtractSchema builds the Ollama structured-output JSON schema for an
// object {"rows":[ {field...} ]}.
func buildExtractSchema(fields []core.Field) json.RawMessage {
	props := map[string]any{}
	required := make([]string, 0, len(fields))
	for _, f := range fields {
		props[f.Name] = map[string]any{"type": jsonType(f.Type), "description": f.Description}
		required = append(required, f.Name)
	}
	item := map[string]any{"type": "object", "properties": props, "required": required}
	root := map[string]any{
		"type":       "object",
		"properties": map[string]any{"rows": map[string]any{"type": "array", "items": item}},
		"required":   []string{"rows"},
	}
	b, _ := json.Marshal(root)
	return b
}

func parseRows(content string, schema []core.Field) ([]map[string]any, error) {
	var parsed struct {
		Rows []map[string]any `json:"rows"`
	}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return nil, fmt.Errorf("decode model output: %w", err)
	}
	for _, row := range parsed.Rows {
		for _, f := range schema {
			if v, ok := row[f.Name]; ok {
				row[f.Name] = coerce(v, f.Type)
			}
		}
	}
	return parsed.Rows, nil
}

func coerce(v any, t core.FieldType) any {
	switch t {
	case core.FieldInt:
		if n, ok := toInt(v); ok {
			return n
		}
	case core.FieldFloat:
		if f, ok := v.(float64); ok {
			return f
		}
	}
	return v
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
