// Package replanner is the LLM-backed engine.Replanner: on a failed step it
// asks the model to adapt (patch the step), skip it, or abort the run.
package replanner

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cole/fetch/internal/agent"
	"github.com/cole/fetch/internal/config"
	"github.com/cole/fetch/internal/core"
	"github.com/cole/fetch/internal/engine"
)

// Replanner decides how to recover a failed step using the LLM.
type Replanner struct {
	llm agent.LLM
	cfg config.Config
}

// New builds a Replanner.
func New(llm agent.LLM, cfg config.Config) *Replanner {
	return &Replanner{llm: llm, cfg: cfg}
}

type replanReply struct {
	Action string     `json:"action"`
	Reason string     `json:"reason"`
	Step   *core.Step `json:"step"`
}

func replanSchema() json.RawMessage {
	step := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":         map[string]any{"type": "string"},
			"name":       map[string]any{"type": "string"},
			"type":       map[string]any{"type": "string"},
			"params":     map[string]any{"type": "object"},
			"depends_on": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
		"required": []string{"id", "name", "type", "params"},
	}
	root := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{"type": "string", "enum": []string{"adapt", "skip", "abort"}},
			"reason": map[string]any{"type": "string"},
			"step":   step,
		},
		"required": []string{"action", "reason"},
	}
	b, _ := json.Marshal(root)
	return b
}

func systemPrompt() string {
	return "You repair a failed step in a running web research pipeline. Given the failed step, its " +
		"resolved params, the attempt number, and the error, choose an action: \"adapt\" (return a " +
		"corrected version of the SAME step with the same id and type but adjusted params, e.g. a " +
		"broadened search query or a different url), \"skip\" (the step is non-essential; continue " +
		"without it), or \"abort\" (unrecoverable). Return action, a short reason, and for adapt the " +
		"full corrected step."
}

func userPrompt(req engine.ReplanRequest) string {
	stepJSON, _ := json.Marshal(req.Step)
	paramsJSON, _ := json.Marshal(req.Params)
	var b strings.Builder
	fmt.Fprintf(&b, "Failed step: %s\nResolved params: %s\nAttempt: %d\nError: %s\n",
		stepJSON, paramsJSON, req.Attempt, req.Err)
	b.WriteString("\nChoose adapt, skip, or abort.")
	return b.String()
}

// Replan asks the model how to recover. A malformed reply maps to abort.
func (r *Replanner) Replan(ctx context.Context, req engine.ReplanRequest) (engine.Decision, error) {
	resp, err := r.llm.Chat(ctx, agent.ChatRequest{
		Model: r.cfg.ModelFor(config.RoleReplan),
		Messages: []agent.Message{
			{Role: "system", Content: systemPrompt()},
			{Role: "user", Content: userPrompt(req)},
		},
		Format: replanSchema(),
	})
	if err != nil {
		return engine.Decision{}, err
	}
	var reply replanReply
	if err := json.Unmarshal([]byte(resp.Content), &reply); err != nil {
		return engine.Decision{Action: engine.ActionAbort, Reason: "could not parse replanner reply"}, nil
	}
	switch reply.Action {
	case "adapt":
		if reply.Step == nil {
			return engine.Decision{Action: engine.ActionAbort, Reason: "adapt without a step"}, nil
		}
		patched := *reply.Step
		patched.ID = req.Step.ID     // force same id
		patched.Type = req.Step.Type // force same type
		return engine.Decision{Action: engine.ActionAdapt, Step: patched, Reason: reply.Reason}, nil
	case "skip":
		return engine.Decision{Action: engine.ActionSkip, Reason: reply.Reason}, nil
	default:
		return engine.Decision{Action: engine.ActionAbort, Reason: reply.Reason}, nil
	}
}
