package builder

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cole/fetch/internal/core"
)

// fieldTypeEnum is the set of allowed field/input types (mirrors core.FieldType).
var fieldTypeEnum = []string{"string", "int", "float", "bool", "timestamp"}

func fieldSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":        map[string]any{"type": "string"},
			"type":        map[string]any{"type": "string", "enum": fieldTypeEnum},
			"description": map[string]any{"type": "string"},
		},
		"required": []string{"name", "type", "description"},
	}
}

func inputParamSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":        map[string]any{"type": "string"},
			"type":        map[string]any{"type": "string", "enum": fieldTypeEnum},
			"required":    map[string]any{"type": "boolean"},
			"description": map[string]any{"type": "string"},
		},
		"required": []string{"name", "type", "required", "description"},
	}
}

func factsSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"domain":        map[string]any{"type": "string"},
			"inputs":        map[string]any{"type": "array", "items": inputParamSchema()},
			"output_fields": map[string]any{"type": "array", "items": fieldSchema()},
			"source_hints":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
		"required": []string{"domain", "inputs", "output_fields", "source_hints"},
	}
}

func rawSchema(root map[string]any) json.RawMessage {
	b, _ := json.Marshal(root)
	return b
}

func interviewReplySchema() json.RawMessage {
	return rawSchema(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"question": map[string]any{"type": "string"},
			"ready":    map[string]any{"type": "boolean"},
			"facts":    factsSchema(),
		},
		"required": []string{"question", "ready", "facts"},
	})
}

func interviewSystemPrompt() string {
	return "You are an interviewer that helps a user define a web research/scraping pipeline. " +
		"Ask exactly ONE clarifying question at a time. Gather four things: " +
		"(1) the entity/domain, (2) the inputs the user provides on each run, " +
		"(3) the output fields they want per result row, (4) source hints or constraints " +
		"(sites, time ranges). When you have enough to design the pipeline, set ready=true and " +
		"leave question empty. Always return your best current understanding in facts. " +
		"Field and input types must be one of: string, int, float, bool, timestamp."
}

func renderTranscript(turns []Turn) string {
	if len(turns) == 0 {
		return "(none)"
	}
	var b strings.Builder
	for _, t := range turns {
		fmt.Fprintf(&b, "%s: %s\n", t.Role, t.Content)
	}
	return b.String()
}

func interviewUserPrompt(st InterviewState) string {
	return fmt.Sprintf("Goal: %s\n\nConversation so far:\n%s\nReturn the next question (or ready=true) and your current facts.",
		st.Goal, renderTranscript(st.Transcript))
}

// factsJSON renders facts compactly for inclusion in design/plan prompts.
func factsJSON(f Facts) string {
	b, _ := json.Marshal(f)
	return string(b)
}

func fieldNames(fields []core.Field) string {
	names := make([]string, len(fields))
	for i, f := range fields {
		names[i] = f.Name
	}
	return strings.Join(names, ", ")
}

func inputNames(inputs []core.InputParam) string {
	names := make([]string, len(inputs))
	for i, in := range inputs {
		names[i] = in.Name
	}
	return strings.Join(names, ", ")
}
