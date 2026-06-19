// Package agent wraps the Ollama HTTP API and (in later plans) the
// role-specific prompt flows built on top of it.
package agent

import (
	"context"
	"encoding/json"
)

// Message is one chat turn.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest is a model-agnostic chat call. Format, when non-nil, is a JSON
// schema passed to Ollama's structured-output `format` parameter.
type ChatRequest struct {
	Model       string
	Messages    []Message
	Format      json.RawMessage
	Temperature float64
}

// ChatResponse is the assistant reply plus token accounting.
type ChatResponse struct {
	Content          string
	PromptTokens     int
	CompletionTokens int
}

// LLM is the chat capability the rest of fetch depends on.
type LLM interface {
	Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
}
