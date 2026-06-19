package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

var _ LLM = (*Ollama)(nil)

// Ollama is an LLM backed by a local Ollama server's /api/chat endpoint.
type Ollama struct {
	baseURL string
	hc      *http.Client
}

// NewOllama builds a client. hc may be nil (http.DefaultClient is used).
func NewOllama(baseURL string, hc *http.Client) *Ollama {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Ollama{baseURL: strings.TrimRight(baseURL, "/"), hc: hc}
}

type ollamaChatBody struct {
	Model    string          `json:"model"`
	Messages []Message       `json:"messages"`
	Stream   bool            `json:"stream"`
	Format   json.RawMessage `json:"format,omitempty"`
	Options  map[string]any  `json:"options,omitempty"`
}

type ollamaChatResp struct {
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
	PromptEvalCount int `json:"prompt_eval_count"`
	EvalCount       int `json:"eval_count"`
}

func (o *Ollama) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	body := ollamaChatBody{
		Model:    req.Model,
		Messages: req.Messages,
		Stream:   false,
		Format:   req.Format,
	}
	if req.Temperature > 0 {
		body.Options = map[string]any{"temperature": req.Temperature}
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return ChatResponse{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/api/chat", bytes.NewReader(buf))
	if err != nil {
		return ChatResponse{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := o.hc.Do(httpReq)
	if err != nil {
		return ChatResponse{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return ChatResponse{}, fmt.Errorf("ollama chat: status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var parsed ollamaChatResp
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return ChatResponse{}, fmt.Errorf("ollama chat: decode: %w", err)
	}
	return ChatResponse{
		Content:          parsed.Message.Content,
		PromptTokens:     parsed.PromptEvalCount,
		CompletionTokens: parsed.EvalCount,
	}, nil
}
