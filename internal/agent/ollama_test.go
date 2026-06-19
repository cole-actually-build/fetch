package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOllamaChatParsesResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"stream":false`) {
			t.Errorf("expected stream:false, got %s", body)
		}
		if !strings.Contains(string(body), `"model":"test-model"`) {
			t.Errorf("model not sent: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"message":{"role":"assistant","content":"hello world"},"prompt_eval_count":12,"eval_count":3,"done":true}`)
	}))
	defer srv.Close()

	c := NewOllama(srv.URL, srv.Client())
	resp, err := c.Chat(context.Background(), ChatRequest{
		Model:    "test-model",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if resp.Content != "hello world" {
		t.Fatalf("content: %q", resp.Content)
	}
	if resp.PromptTokens != 12 || resp.CompletionTokens != 3 {
		t.Fatalf("tokens: %d/%d", resp.PromptTokens, resp.CompletionTokens)
	}
}

func TestOllamaChatSendsFormat(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = string(b)
		io.WriteString(w, `{"message":{"content":"{}"},"done":true}`)
	}))
	defer srv.Close()

	c := NewOllama(srv.URL, srv.Client())
	_, err := c.Chat(context.Background(), ChatRequest{
		Model:    "m",
		Messages: []Message{{Role: "user", Content: "x"}},
		Format:   json.RawMessage(`{"type":"object"}`),
	})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if !strings.Contains(got, `"format":{"type":"object"}`) {
		t.Fatalf("format not forwarded: %s", got)
	}
}

func TestOllamaChatErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "model not found", http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewOllama(srv.URL, srv.Client())
	_, err := c.Chat(context.Background(), ChatRequest{Model: "nope", Messages: []Message{{Role: "user", Content: "x"}}})
	if err == nil {
		t.Fatal("expected error on non-200")
	}
}
