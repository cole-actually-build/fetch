# Fetch Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the tested foundation layer of `fetch` — core domain types, config loading, an Ollama LLM client, and the four capability providers (Tavily search, HTTP fetch, disk artifacts, DuckDB store) — each behind an interface with deterministic tests.

**Architecture:** Pure-data domain types live in `internal/core` with no dependencies. Each external capability (LLM, search, fetch, artifacts, rows) is an interface in its own package with a concrete implementation and a fake. Concrete impls take an injectable base URL / root dir so they test against `httptest` servers and temp directories — no network or real models required for the unit suite.

**Tech Stack:** Go 1.26, `database/sql` + `github.com/marcboeker/go-duckdb` (cgo), `github.com/PuerkitoBio/goquery`, `github.com/go-shiori/go-readability`, `github.com/BurntSushi/toml`. Stdlib `net/http` + `net/http/httptest` for clients and tests.

This is **Plan 1 of 4** for v1 (Foundation → Engine → Agents → TUI). It produces no end-user UI; its deliverable is a set of tested packages the Engine plan consumes.

## Global Constraints

- Module path: `github.com/cole/fetch` (already `go mod init`'d).
- Go version floor: `go 1.26` (matches installed `go1.26.4`).
- `CGO_ENABLED=1` is required (DuckDB). The toolchain is `cc`/clang 17, already installed.
- All packages live under `internal/`. The only `main` is `cmd/fetch`.
- Every external-dependency type (LLM, Search, Fetcher, artifact Store, row Store) is defined as an **interface** in its package; concrete impls are constructed via a `New(...)` function that accepts injectable endpoints (base URL or root dir) for testability.
- Domain types are defined **once** in `internal/core` and imported everywhere; do not redeclare `Field`, `Step`, `Pipeline`, `Run`, or `StepTrace`.
- Tests must not hit the network or a real Ollama/Tavily endpoint. Real-endpoint checks go behind the `//go:build integration` tag and are not part of `go test ./...`.
- Run `gofmt`/`go vet ./...` clean before each commit.

---

### Task 1: Module scaffold, core domain types, and smoke build

**Files:**
- Create: `cmd/fetch/main.go`
- Create: `internal/core/types.go`
- Create: `internal/core/types_test.go`
- Create: `.gitignore`
- Modify: `go.mod` (via `go mod tidy`)

**Interfaces:**
- Consumes: nothing.
- Produces: package `core` with `FieldType` constants (`FieldString`, `FieldInt`, `FieldFloat`, `FieldBool`, `FieldTimestamp`), and structs `Field{Name string; Type FieldType; Description string}`, `InputParam{Name string; Type FieldType; Required bool; Description string}`, `StepType` constants (`StepSearch`,`StepFetch`,`StepExtract`,`StepTransform`,`StepStore`), `Step{ID,Name string; Type StepType; Params map[string]any; DependsOn []string}`, `Pipeline{...}`, `RunStatus` constants (`RunRunning`,`RunOK`,`RunFailed`,`RunPartial`), `Run{...}`, `StepTrace{...}`. All structs carry JSON tags. `Pipeline` round-trips through `encoding/json` losslessly.

- [ ] **Step 1: Write `.gitignore`**

```gitignore
/fetch
/bin/
*.duckdb
*.duckdb.wal
/data/
.DS_Store
```

- [ ] **Step 2: Write the failing test**

Create `internal/core/types_test.go`:

```go
package core

import (
	"encoding/json"
	"testing"
	"time"
)

func TestPipelineJSONRoundTrip(t *testing.T) {
	in := Pipeline{
		ID:          "p1",
		Name:        "Truck part cross-ref",
		Description: "Find cross references for a part number",
		Domain:      "truck-parts",
		Inputs:      []InputParam{{Name: "part_number", Type: FieldString, Required: true, Description: "OEM part #"}},
		Schema:      []Field{{Name: "cross_ref", Type: FieldString, Description: "cross reference number"}},
		Plan: []Step{{
			ID:        "s1",
			Name:      "search",
			Type:      StepSearch,
			Params:    map[string]any{"query": "{{input.part_number}} cross reference"},
			DependsOn: nil,
		}},
		Models:    map[string]string{"extract": "qwen3-coder:latest"},
		CreatedAt: time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC),
		Version:   1,
	}

	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out Pipeline
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.ID != in.ID || out.Version != in.Version || len(out.Plan) != 1 {
		t.Fatalf("round trip mismatch: %+v", out)
	}
	if out.Plan[0].Type != StepSearch {
		t.Fatalf("step type lost: %q", out.Plan[0].Type)
	}
	if out.Schema[0].Type != FieldString {
		t.Fatalf("field type lost: %q", out.Schema[0].Type)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/core/`
Expected: FAIL — `internal/core/types.go` does not exist, build error (`undefined: Pipeline`).

- [ ] **Step 4: Write `internal/core/types.go`**

```go
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
```

- [ ] **Step 5: Write `cmd/fetch/main.go`** (minimal, so the module builds)

```go
package main

import "fmt"

func main() {
	fmt.Println("fetch — agentic web research pipelines (foundation build)")
}
```

- [ ] **Step 6: Run tests and build to verify they pass**

Run: `go test ./internal/core/ && go build ./... && go vet ./...`
Expected: PASS, build succeeds, vet clean.

- [ ] **Step 7: Commit**

```bash
git init
git add .gitignore go.mod cmd/fetch/main.go internal/core/
git commit -m "feat: core domain types and module scaffold"
```

---

### Task 2: Config loading

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`
- Create: `internal/config/testdata/config.toml`
- Modify: `go.mod` (adds `github.com/BurntSushi/toml`)

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces:
  - Role constants: `RoleInterview, RolePlan, RoleSchema, RoleExtract, RoleReplan, RoleRoute string`.
  - `type Config struct { DataDir string; Models map[string]string; Ollama OllamaConfig; Search SearchConfig; Fetch FetchConfig }`
  - `type OllamaConfig struct { BaseURL string }`
  - `type SearchConfig struct { Provider string; BaseURL string; APIKeyEnv string; MaxResults int }`
  - `type FetchConfig struct { UserAgent string; TimeoutSeconds int; MaxBytes int64 }`
  - `func Default() Config` — fully-populated defaults (Ollama `http://localhost:11434`, Tavily provider, the three installed models mapped to roles, `MaxResults: 5`).
  - `func Load(path string) (Config, error)` — start from `Default()`, decode the TOML file over it (missing file → defaults, no error), expand `~` in `DataDir`.
  - `func (c Config) ModelFor(role string) string` — returns the mapped model, falling back to `Models[RouteRole]` then a hard default.
  - `func (c Config) APIKey() string` — reads `os.Getenv(c.Search.APIKeyEnv)`.

- [ ] **Step 1: Write test fixture `internal/config/testdata/config.toml`**

```toml
data_dir = "/tmp/fetch-test"

[models]
interview = "gpt-oss:20b"
extract = "qwen3-coder:latest"

[ollama]
base_url = "http://localhost:9999"

[search]
provider = "tavily"
max_results = 8
```

- [ ] **Step 2: Write the failing test**

Create `internal/config/config_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultPopulatesRoles(t *testing.T) {
	c := Default()
	if c.ModelFor(RoleExtract) == "" {
		t.Fatal("expected a default extract model")
	}
	if c.Ollama.BaseURL == "" {
		t.Fatal("expected default ollama base url")
	}
	if c.Search.MaxResults <= 0 {
		t.Fatal("expected positive default max results")
	}
}

func TestLoadOverlaysOntoDefaults(t *testing.T) {
	c, err := Load(filepath.Join("testdata", "config.toml"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// Overridden by file:
	if c.Ollama.BaseURL != "http://localhost:9999" {
		t.Fatalf("base url not overridden: %q", c.Ollama.BaseURL)
	}
	if c.Search.MaxResults != 8 {
		t.Fatalf("max results not overridden: %d", c.Search.MaxResults)
	}
	if c.ModelFor(RoleInterview) != "gpt-oss:20b" {
		t.Fatalf("interview model: %q", c.ModelFor(RoleInterview))
	}
	// Not in file → still defaulted:
	if c.ModelFor(RoleReplan) == "" {
		t.Fatal("replan role should fall back to a default")
	}
}

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	c, err := Load(filepath.Join("testdata", "does-not-exist.toml"))
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if c.Ollama.BaseURL == "" {
		t.Fatal("expected defaults for missing file")
	}
}

func TestAPIKeyReadsEnv(t *testing.T) {
	c := Default()
	c.Search.APIKeyEnv = "FETCH_TEST_KEY"
	os.Setenv("FETCH_TEST_KEY", "secret")
	defer os.Unsetenv("FETCH_TEST_KEY")
	if c.APIKey() != "secret" {
		t.Fatalf("api key: %q", c.APIKey())
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/config/`
Expected: FAIL — `undefined: Default`, `undefined: Load`.

- [ ] **Step 4: Add the toml dependency**

Run: `go get github.com/BurntSushi/toml@latest`
Expected: adds the require line to `go.mod`.

- [ ] **Step 5: Write `internal/config/config.go`**

```go
// Package config loads fetch's runtime configuration: where data lives,
// which Ollama model serves each role, and provider endpoints/keys.
package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Roles map units of agent work to models.
const (
	RoleInterview = "interview"
	RolePlan      = "plan"
	RoleSchema    = "schema"
	RoleExtract   = "extract"
	RoleReplan    = "replan"
	RoleRoute     = "route"
)

type Config struct {
	DataDir string            `toml:"data_dir"`
	Models  map[string]string `toml:"models"`
	Ollama  OllamaConfig      `toml:"ollama"`
	Search  SearchConfig      `toml:"search"`
	Fetch   FetchConfig       `toml:"fetch"`
}

type OllamaConfig struct {
	BaseURL string `toml:"base_url"`
}

type SearchConfig struct {
	Provider   string `toml:"provider"`
	BaseURL    string `toml:"base_url"`
	APIKeyEnv  string `toml:"api_key_env"`
	MaxResults int    `toml:"max_results"`
}

type FetchConfig struct {
	UserAgent      string `toml:"user_agent"`
	TimeoutSeconds int    `toml:"timeout_seconds"`
	MaxBytes       int64  `toml:"max_bytes"`
}

// Default returns a fully-populated config. Load overlays a file onto this.
func Default() Config {
	return Config{
		DataDir: "~/.local/share/fetch",
		Models: map[string]string{
			RoleInterview: "gpt-oss:20b",
			RolePlan:      "gpt-oss:20b",
			RoleSchema:    "qwen3-coder:latest",
			RoleExtract:   "qwen3-coder:latest",
			RoleReplan:    "gpt-oss:20b",
			RoleRoute:     "llama3.2:latest",
		},
		Ollama: OllamaConfig{BaseURL: "http://localhost:11434"},
		Search: SearchConfig{
			Provider:   "tavily",
			BaseURL:    "https://api.tavily.com",
			APIKeyEnv:  "TAVILY_API_KEY",
			MaxResults: 5,
		},
		Fetch: FetchConfig{
			UserAgent:      "fetch/0.1 (+https://github.com/cole/fetch)",
			TimeoutSeconds: 30,
			MaxBytes:       5 << 20, // 5 MiB
		},
	}
}

// Load starts from Default and decodes the TOML file at path over it.
// A missing file is not an error: defaults are returned.
func Load(path string) (Config, error) {
	c := Default()
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return expand(c), nil
		}
		return c, err
	}
	if _, err := toml.DecodeFile(path, &c); err != nil {
		return c, err
	}
	// A models table in the file replaces individual keys it sets but should
	// not wipe roles it omits. Merge file values over defaults.
	merged := Default().Models
	for k, v := range c.Models {
		merged[k] = v
	}
	c.Models = merged
	return expand(c), nil
}

func expand(c Config) Config {
	if strings.HasPrefix(c.DataDir, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			c.DataDir = filepath.Join(home, strings.TrimPrefix(c.DataDir, "~"))
		}
	}
	return c
}

// ModelFor returns the model assigned to a role, falling back to the route
// model and then a final hard default so callers never get an empty string.
func (c Config) ModelFor(role string) string {
	if m, ok := c.Models[role]; ok && m != "" {
		return m
	}
	if m, ok := c.Models[RoleRoute]; ok && m != "" {
		return m
	}
	return "llama3.2:latest"
}

// APIKey reads the search provider's API key from the configured env var.
func (c Config) APIKey() string {
	if c.Search.APIKeyEnv == "" {
		return ""
	}
	return os.Getenv(c.Search.APIKeyEnv)
}
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/config/ && go vet ./internal/config/`
Expected: PASS, vet clean.

- [ ] **Step 7: Commit**

```bash
go mod tidy
git add internal/config/ go.mod go.sum
git commit -m "feat: config loading with role->model map and provider settings"
```

---

### Task 3: Ollama LLM client

**Files:**
- Create: `internal/agent/llm.go`
- Create: `internal/agent/ollama.go`
- Create: `internal/agent/ollama_test.go`
- Create: `internal/agent/fake.go`

**Interfaces:**
- Consumes: nothing from earlier tasks (uses stdlib only).
- Produces:
  - `type Message struct { Role string; Content string }`
  - `type ChatRequest struct { Model string; Messages []Message; Format json.RawMessage; Temperature float64 }`
  - `type ChatResponse struct { Content string; PromptTokens int; CompletionTokens int }`
  - `type LLM interface { Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) }`
  - `func NewOllama(baseURL string, hc *http.Client) *Ollama` and `*Ollama` implements `LLM` by POSTing `/api/chat` with `stream:false`.
  - `type FakeLLM struct { Responses []ChatResponse; Err error; Calls []ChatRequest }` implementing `LLM`, returning queued responses in order (used by later plans).

- [ ] **Step 1: Write the failing test**

Create `internal/agent/ollama_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agent/`
Expected: FAIL — `undefined: NewOllama`.

- [ ] **Step 3: Write `internal/agent/llm.go`**

```go
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
```

- [ ] **Step 4: Write `internal/agent/ollama.go`**

```go
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
```

- [ ] **Step 5: Write `internal/agent/fake.go`**

```go
package agent

import "context"

// FakeLLM is a deterministic LLM for tests in this and later plans. It returns
// queued Responses in order; once exhausted it repeats the last one.
type FakeLLM struct {
	Responses []ChatResponse
	Err       error
	Calls     []ChatRequest
}

func (f *FakeLLM) Chat(_ context.Context, req ChatRequest) (ChatResponse, error) {
	f.Calls = append(f.Calls, req)
	if f.Err != nil {
		return ChatResponse{}, f.Err
	}
	if len(f.Responses) == 0 {
		return ChatResponse{}, nil
	}
	idx := len(f.Calls) - 1
	if idx >= len(f.Responses) {
		idx = len(f.Responses) - 1
	}
	return f.Responses[idx], nil
}
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/agent/ && go vet ./internal/agent/`
Expected: PASS, vet clean.

- [ ] **Step 7: Commit**

```bash
git add internal/agent/
git commit -m "feat: ollama LLM client with structured-output support and fake"
```

---

### Task 4: Search provider interface + Tavily

**Files:**
- Create: `internal/providers/search/search.go`
- Create: `internal/providers/search/tavily.go`
- Create: `internal/providers/search/tavily_test.go`
- Create: `internal/providers/search/fake.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type Result struct { Title string; URL string; Snippet string; Content string; Score float64 }`
  - `type Options struct { MaxResults int }`
  - `type Search interface { Search(ctx context.Context, query string, opts Options) ([]Result, error) }`
  - `func NewTavily(baseURL, apiKey string, hc *http.Client) *Tavily` implementing `Search` against `POST {baseURL}/search`.
  - `type FakeSearch struct { Results []Result; Err error; Queries []string }` implementing `Search`.

- [ ] **Step 1: Write the failing test**

Create `internal/providers/search/tavily_test.go`:

```go
package search

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTavilySearchParsesResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		json.Unmarshal(raw, &body)
		if body["query"] != "truck part 12345" {
			t.Errorf("query not forwarded: %v", body["query"])
		}
		if body["api_key"] != "key-abc" {
			t.Errorf("api key not forwarded: %v", body["api_key"])
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"results":[
			{"title":"Cross Ref","url":"https://ex.com/a","content":"snippet a","score":0.9,"raw_content":"full a"},
			{"title":"Catalog","url":"https://ex.com/b","content":"snippet b","score":0.5}
		]}`)
	}))
	defer srv.Close()

	s := NewTavily(srv.URL, "key-abc", srv.Client())
	got, err := s.Search(context.Background(), "truck part 12345", Options{MaxResults: 5})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 results, got %d", len(got))
	}
	if got[0].URL != "https://ex.com/a" || got[0].Score != 0.9 {
		t.Fatalf("result 0 wrong: %+v", got[0])
	}
	if got[0].Content != "full a" {
		t.Fatalf("expected raw_content preferred for Content, got %q", got[0].Content)
	}
	if got[1].Content != "snippet b" {
		t.Fatalf("expected content fallback, got %q", got[1].Content)
	}
}

func TestTavilySearchErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()
	s := NewTavily(srv.URL, "bad", srv.Client())
	if _, err := s.Search(context.Background(), "q", Options{}); err == nil {
		t.Fatal("expected error on 401")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/providers/search/`
Expected: FAIL — `undefined: NewTavily`.

- [ ] **Step 3: Write `internal/providers/search/search.go`**

```go
// Package search defines the discovery capability and its Tavily backend.
package search

import "context"

// Result is one search hit. Content holds the fullest text available
// (provider raw content when present, else the snippet).
type Result struct {
	Title   string
	URL     string
	Snippet string
	Content string
	Score   float64
}

// Options tune a search call.
type Options struct {
	MaxResults int
}

// Search discovers candidate URLs for a query.
type Search interface {
	Search(ctx context.Context, query string, opts Options) ([]Result, error)
}
```

- [ ] **Step 4: Write `internal/providers/search/tavily.go`**

```go
package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Tavily implements Search via the Tavily REST API.
type Tavily struct {
	baseURL string
	apiKey  string
	hc      *http.Client
}

func NewTavily(baseURL, apiKey string, hc *http.Client) *Tavily {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Tavily{baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, hc: hc}
}

type tavilyReq struct {
	APIKey            string `json:"api_key"`
	Query             string `json:"query"`
	MaxResults        int    `json:"max_results"`
	IncludeRawContent bool   `json:"include_raw_content"`
}

type tavilyResp struct {
	Results []struct {
		Title      string  `json:"title"`
		URL        string  `json:"url"`
		Content    string  `json:"content"`
		RawContent string  `json:"raw_content"`
		Score      float64 `json:"score"`
	} `json:"results"`
}

func (t *Tavily) Search(ctx context.Context, query string, opts Options) ([]Result, error) {
	max := opts.MaxResults
	if max <= 0 {
		max = 5
	}
	buf, err := json.Marshal(tavilyReq{
		APIKey:            t.apiKey,
		Query:             query,
		MaxResults:        max,
		IncludeRawContent: true,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL+"/search", bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tavily search: status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var parsed tavilyResp
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("tavily search: decode: %w", err)
	}
	out := make([]Result, 0, len(parsed.Results))
	for _, r := range parsed.Results {
		content := r.RawContent
		if content == "" {
			content = r.Content
		}
		out = append(out, Result{
			Title:   r.Title,
			URL:     r.URL,
			Snippet: r.Content,
			Content: content,
			Score:   r.Score,
		})
	}
	return out, nil
}
```

- [ ] **Step 5: Write `internal/providers/search/fake.go`**

```go
package search

import "context"

// FakeSearch returns canned results and records queries, for tests.
type FakeSearch struct {
	Results []Result
	Err     error
	Queries []string
}

func (f *FakeSearch) Search(_ context.Context, query string, _ Options) ([]Result, error) {
	f.Queries = append(f.Queries, query)
	if f.Err != nil {
		return nil, f.Err
	}
	return f.Results, nil
}
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/providers/search/ && go vet ./internal/providers/search/`
Expected: PASS, vet clean.

- [ ] **Step 7: Commit**

```bash
git add internal/providers/search/
git commit -m "feat: search interface with Tavily backend and fake"
```

---

### Task 5: Fetcher interface + HTTP fetcher

**Files:**
- Create: `internal/providers/fetch/fetch.go`
- Create: `internal/providers/fetch/http.go`
- Create: `internal/providers/fetch/http_test.go`
- Create: `internal/providers/fetch/fake.go`
- Modify: `go.mod` (adds `github.com/go-shiori/go-readability`)

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type Method string` with `MethodHTTP Method = "http"` and `MethodBrowser Method = "browser"`.
  - `var ErrMethodUnavailable = errors.New("fetch: method unavailable")`.
  - `type Page struct { URL string; StatusCode int; ContentType string; Raw []byte; Text string }`
  - `type Fetcher interface { Fetch(ctx context.Context, url string, method Method) (Page, error); Methods() []Method }`
  - `func NewHTTP(userAgent string, timeoutSeconds int, maxBytes int64) *HTTPFetcher` implementing `Fetcher`; supports `MethodHTTP`, returns `ErrMethodUnavailable` for `MethodBrowser`. For HTML responses it fills `Text` via go-readability; for non-HTML it sets `Text` to the raw string.
  - `type FakeFetcher struct { Pages map[string]Page; Err error; URLs []string }` implementing `Fetcher`.

- [ ] **Step 1: Write the failing test**

Create `internal/providers/fetch/http_test.go`:

```go
package fetch

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPFetchReadabilityText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, `<html><head><title>Part 123</title></head><body>
			<nav>menu noise</nav>
			<article><h1>Cross Reference</h1><p>The cross reference for part 123 is XYZ-999.</p></article>
		</body></html>`)
	}))
	defer srv.Close()

	f := NewHTTP("test-agent", 10, 1<<20)
	page, err := f.Fetch(context.Background(), srv.URL, MethodHTTP)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if page.StatusCode != 200 {
		t.Fatalf("status: %d", page.StatusCode)
	}
	if len(page.Raw) == 0 {
		t.Fatal("expected raw bytes")
	}
	if !strings.Contains(page.Text, "XYZ-999") {
		t.Fatalf("readability text missing content: %q", page.Text)
	}
}

func TestHTTPFetchBrowserUnavailable(t *testing.T) {
	f := NewHTTP("test-agent", 10, 1<<20)
	_, err := f.Fetch(context.Background(), "http://example.com", MethodBrowser)
	if !errors.Is(err, ErrMethodUnavailable) {
		t.Fatalf("expected ErrMethodUnavailable, got %v", err)
	}
}

func TestHTTPFetchMethods(t *testing.T) {
	f := NewHTTP("ua", 10, 1<<20)
	ms := f.Methods()
	if len(ms) != 1 || ms[0] != MethodHTTP {
		t.Fatalf("methods: %v", ms)
	}
}
```

Note: the test references `io.WriteString`; add `"io"` to the import block when writing the file (kept out of the snippet above only to highlight the assertions). Final import block:

```go
import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/providers/fetch/`
Expected: FAIL — `undefined: NewHTTP`.

- [ ] **Step 3: Add the readability dependency**

Run: `go get github.com/go-shiori/go-readability@latest`
Expected: adds the require line.

- [ ] **Step 4: Write `internal/providers/fetch/fetch.go`**

```go
// Package fetch defines the page-fetch capability. v1 ships an HTTP backend;
// a headless-browser backend is a later layer behind the same interface.
package fetch

import (
	"context"
	"errors"
)

// Method selects how a page is retrieved.
type Method string

const (
	MethodHTTP    Method = "http"
	MethodBrowser Method = "browser"
)

// ErrMethodUnavailable is returned when a Fetcher is asked for a method it
// does not implement (e.g. browser in v1).
var ErrMethodUnavailable = errors.New("fetch: method unavailable")

// Page is a fetched document. Raw is the original bytes (stored as an
// artifact); Text is cleaned main-content for extraction.
type Page struct {
	URL         string
	StatusCode  int
	ContentType string
	Raw         []byte
	Text        string
}

// Fetcher retrieves a single URL using the requested method.
type Fetcher interface {
	Fetch(ctx context.Context, url string, method Method) (Page, error)
	Methods() []Method
}
```

- [ ] **Step 5: Write `internal/providers/fetch/http.go`**

```go
package fetch

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	readability "github.com/go-shiori/go-readability"
)

// HTTPFetcher retrieves pages with a plain HTTP GET.
type HTTPFetcher struct {
	userAgent string
	maxBytes  int64
	hc        *http.Client
}

func NewHTTP(userAgent string, timeoutSeconds int, maxBytes int64) *HTTPFetcher {
	if maxBytes <= 0 {
		maxBytes = 5 << 20
	}
	return &HTTPFetcher{
		userAgent: userAgent,
		maxBytes:  maxBytes,
		hc:        &http.Client{Timeout: time.Duration(timeoutSeconds) * time.Second},
	}
}

func (h *HTTPFetcher) Methods() []Method { return []Method{MethodHTTP} }

func (h *HTTPFetcher) Fetch(ctx context.Context, rawURL string, method Method) (Page, error) {
	if method != MethodHTTP {
		return Page{}, fmt.Errorf("%w: %s", ErrMethodUnavailable, method)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return Page{}, err
	}
	req.Header.Set("User-Agent", h.userAgent)

	resp, err := h.hc.Do(req)
	if err != nil {
		return Page{}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, h.maxBytes))
	if err != nil {
		return Page{}, err
	}
	ct := resp.Header.Get("Content-Type")
	page := Page{
		URL:         rawURL,
		StatusCode:  resp.StatusCode,
		ContentType: ct,
		Raw:         body,
	}
	if strings.Contains(ct, "html") {
		page.Text = extractReadable(body, rawURL)
	} else {
		page.Text = string(body)
	}
	return page, nil
}

func extractReadable(body []byte, rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return string(body)
	}
	article, err := readability.FromReader(bytes.NewReader(body), parsed)
	if err != nil || strings.TrimSpace(article.TextContent) == "" {
		return string(body)
	}
	return strings.TrimSpace(article.TextContent)
}
```

- [ ] **Step 6: Write `internal/providers/fetch/fake.go`**

```go
package fetch

import "context"

// FakeFetcher serves canned pages keyed by URL, for tests.
type FakeFetcher struct {
	Pages map[string]Page
	Err   error
	URLs  []string
}

func (f *FakeFetcher) Methods() []Method { return []Method{MethodHTTP} }

func (f *FakeFetcher) Fetch(_ context.Context, url string, _ Method) (Page, error) {
	f.URLs = append(f.URLs, url)
	if f.Err != nil {
		return Page{}, f.Err
	}
	if p, ok := f.Pages[url]; ok {
		return p, nil
	}
	return Page{URL: url, StatusCode: 404}, nil
}
```

- [ ] **Step 7: Run tests to verify they pass**

Run: `go test ./internal/providers/fetch/ && go vet ./internal/providers/fetch/`
Expected: PASS, vet clean.

- [ ] **Step 8: Commit**

```bash
go mod tidy
git add internal/providers/fetch/ go.mod go.sum
git commit -m "feat: fetcher interface with HTTP backend, readability, and fake"
```

---

### Task 6: Artifact store (disk)

**Files:**
- Create: `internal/providers/artifacts/artifacts.go`
- Create: `internal/providers/artifacts/disk.go`
- Create: `internal/providers/artifacts/disk_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type Store interface { Put(runID, stepID string, data []byte, ext string) (ref string, err error); Get(ref string) ([]byte, error) }`
  - `func NewDisk(root string) *Disk` implementing `Store`. `Put` writes to `{root}/{runID}/{stepID}/{sha256hex}.{ext}` and returns the path **relative to root** as the ref. `Get(ref)` reads `{root}/{ref}`. Content-addressed: identical bytes in the same run+step produce the same ref (idempotent).

- [ ] **Step 1: Write the failing test**

Create `internal/providers/artifacts/disk_test.go`:

```go
package artifacts

import (
	"bytes"
	"testing"
)

func TestDiskPutGetRoundTrip(t *testing.T) {
	d := NewDisk(t.TempDir())
	data := []byte("<html>raw</html>")
	ref, err := d.Put("run1", "stepA", data, "html")
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if ref == "" {
		t.Fatal("empty ref")
	}
	got, err := d.Get(ref)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("round trip mismatch: %q", got)
	}
}

func TestDiskPutIsContentAddressed(t *testing.T) {
	d := NewDisk(t.TempDir())
	r1, _ := d.Put("run1", "stepA", []byte("same"), "json")
	r2, _ := d.Put("run1", "stepA", []byte("same"), "json")
	if r1 != r2 {
		t.Fatalf("identical content should yield identical ref: %q vs %q", r1, r2)
	}
	r3, _ := d.Put("run1", "stepA", []byte("different"), "json")
	if r3 == r1 {
		t.Fatal("different content should yield different ref")
	}
}

func TestDiskGetMissing(t *testing.T) {
	d := NewDisk(t.TempDir())
	if _, err := d.Get("run1/stepA/deadbeef.json"); err == nil {
		t.Fatal("expected error for missing ref")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/providers/artifacts/`
Expected: FAIL — `undefined: NewDisk`.

- [ ] **Step 3: Write `internal/providers/artifacts/artifacts.go`**

```go
// Package artifacts stores raw fetched bytes so extraction can be re-run
// without re-fetching. Refs are paths relative to the store root.
package artifacts

// Store persists and retrieves raw artifact bytes.
type Store interface {
	Put(runID, stepID string, data []byte, ext string) (ref string, err error)
	Get(ref string) ([]byte, error)
}
```

- [ ] **Step 4: Write `internal/providers/artifacts/disk.go`**

```go
package artifacts

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
)

// Disk is a content-addressed artifact store rooted at a directory.
type Disk struct {
	root string
}

func NewDisk(root string) *Disk { return &Disk{root: root} }

func (d *Disk) Put(runID, stepID string, data []byte, ext string) (string, error) {
	sum := sha256.Sum256(data)
	name := hex.EncodeToString(sum[:]) + "." + ext
	rel := filepath.Join(runID, stepID, name)
	full := filepath.Join(d.root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(full, data, 0o644); err != nil {
		return "", err
	}
	return rel, nil
}

func (d *Disk) Get(ref string) ([]byte, error) {
	return os.ReadFile(filepath.Join(d.root, ref))
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/providers/artifacts/ && go vet ./internal/providers/artifacts/`
Expected: PASS, vet clean.

- [ ] **Step 6: Commit**

```bash
git add internal/providers/artifacts/
git commit -m "feat: content-addressed disk artifact store"
```

---

### Task 7: DuckDB row store (validates the cgo build)

**Files:**
- Create: `internal/providers/store/store.go`
- Create: `internal/providers/store/duckdb.go`
- Create: `internal/providers/store/duckdb_test.go`
- Modify: `go.mod` (adds `github.com/marcboeker/go-duckdb`)

**Interfaces:**
- Consumes: `core.Field`, `core.FieldType` constants, `core.Run`, `core.StepTrace` (Task 1).
- Produces:
  - `type Store interface { EnsureTable(pipelineID string, fields []core.Field) error; AppendRows(pipelineID string, fields []core.Field, runID string, rows []map[string]any) error; Query(sql string) ([]map[string]any, error); RecordRun(r core.Run) error; RecordTrace(t core.StepTrace) error; Close() error }`
  - `func OpenDuckDB(path string) (*DuckDB, error)` implementing `Store`. On open it creates the meta tables `runs` and `step_traces`. `EnsureTable` creates `data_{pipelineID}` with the schema fields plus `__run_id VARCHAR` and `__fetched_at TIMESTAMP`. Pipeline IDs are sanitized to `[a-zA-Z0-9_]` for table names.
  - Mapping `core.FieldType` → DuckDB type: string→`VARCHAR`, int→`BIGINT`, float→`DOUBLE`, bool→`BOOLEAN`, timestamp→`TIMESTAMP`.

- [ ] **Step 1: Write the failing test**

Create `internal/providers/store/duckdb_test.go`:

```go
package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/cole/fetch/internal/core"
)

func openTemp(t *testing.T) *DuckDB {
	t.Helper()
	db, err := OpenDuckDB(filepath.Join(t.TempDir(), "test.duckdb"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestEnsureTableAndAppendRows(t *testing.T) {
	db := openTemp(t)
	fields := []core.Field{
		{Name: "cross_ref", Type: core.FieldString},
		{Name: "price", Type: core.FieldFloat},
		{Name: "in_stock", Type: core.FieldBool},
	}
	if err := db.EnsureTable("pipe1", fields); err != nil {
		t.Fatalf("ensure table: %v", err)
	}
	// idempotent
	if err := db.EnsureTable("pipe1", fields); err != nil {
		t.Fatalf("ensure table twice: %v", err)
	}
	rows := []map[string]any{
		{"cross_ref": "XYZ-1", "price": 12.5, "in_stock": true},
		{"cross_ref": "XYZ-2", "price": 9.0, "in_stock": false},
	}
	if err := db.AppendRows("pipe1", fields, "run-1", rows); err != nil {
		t.Fatalf("append: %v", err)
	}
	got, err := db.Query(`SELECT cross_ref, price, in_stock, __run_id FROM data_pipe1 ORDER BY cross_ref`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 rows, got %d", len(got))
	}
	if got[0]["cross_ref"] != "XYZ-1" {
		t.Fatalf("row 0: %+v", got[0])
	}
	if got[0]["__run_id"] != "run-1" {
		t.Fatalf("run id not stamped: %+v", got[0])
	}
}

func TestRecordRunAndTrace(t *testing.T) {
	db := openTemp(t)
	run := core.Run{
		ID: "run-1", PipelineID: "pipe1",
		Input: map[string]any{"part_number": "123"},
		Status: core.RunOK,
		StartedAt: time.Now().UTC(), FinishedAt: time.Now().UTC(),
	}
	if err := db.RecordRun(run); err != nil {
		t.Fatalf("record run: %v", err)
	}
	tr := core.StepTrace{RunID: "run-1", StepID: "s1", Status: "ok", FallbackUsed: true, Tokens: 42}
	if err := db.RecordTrace(tr); err != nil {
		t.Fatalf("record trace: %v", err)
	}
	got, err := db.Query(`SELECT id, status FROM runs WHERE id = 'run-1'`)
	if err != nil {
		t.Fatalf("query runs: %v", err)
	}
	if len(got) != 1 || got[0]["status"] != string(core.RunOK) {
		t.Fatalf("run not recorded: %+v", got)
	}
	traces, err := db.Query(`SELECT step_id, fallback_used FROM step_traces WHERE run_id = 'run-1'`)
	if err != nil {
		t.Fatalf("query traces: %v", err)
	}
	if len(traces) != 1 {
		t.Fatalf("trace not recorded: %+v", traces)
	}
}

func TestSanitizeRejectsInjection(t *testing.T) {
	db := openTemp(t)
	// A pipeline id with punctuation must not break table creation.
	if err := db.EnsureTable("weird-id.drop", []core.Field{{Name: "x", Type: core.FieldString}}); err != nil {
		t.Fatalf("ensure table sanitized: %v", err)
	}
	if err := db.AppendRows("weird-id.drop", []core.Field{{Name: "x", Type: core.FieldString}}, "r", []map[string]any{{"x": "ok"}}); err != nil {
		t.Fatalf("append sanitized: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/providers/store/`
Expected: FAIL — `undefined: OpenDuckDB` (and the dependency is not yet present).

- [ ] **Step 3: Add the DuckDB dependency and confirm it builds with cgo**

Run: `go get github.com/marcboeker/go-duckdb@latest`
Then: `CGO_ENABLED=1 go build ./...`
Expected: dependency added; build succeeds (this is the real cgo/DuckDB toolchain check). First build downloads/compiles the DuckDB static lib and may take a few minutes.

- [ ] **Step 4: Write `internal/providers/store/store.go`**

```go
// Package store persists pipeline output rows and run/trace records in DuckDB.
package store

import "github.com/cole/fetch/internal/core"

// Store is fetch's structured-result and run-history persistence.
type Store interface {
	EnsureTable(pipelineID string, fields []core.Field) error
	AppendRows(pipelineID string, fields []core.Field, runID string, rows []map[string]any) error
	Query(sql string) ([]map[string]any, error)
	RecordRun(r core.Run) error
	RecordTrace(t core.StepTrace) error
	Close() error
}
```

- [ ] **Step 5: Write `internal/providers/store/duckdb.go`**

```go
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cole/fetch/internal/core"
	_ "github.com/marcboeker/go-duckdb"
)

// DuckDB implements Store over a single DuckDB database file.
type DuckDB struct {
	db *sql.DB
}

// OpenDuckDB opens (creating if needed) the database and ensures meta tables.
func OpenDuckDB(path string) (*DuckDB, error) {
	db, err := sql.Open("duckdb", path)
	if err != nil {
		return nil, err
	}
	d := &DuckDB{db: db}
	if err := d.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return d, nil
}

func (d *DuckDB) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS runs (
			id VARCHAR PRIMARY KEY,
			pipeline_id VARCHAR,
			input JSON,
			status VARCHAR,
			started_at TIMESTAMP,
			finished_at TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS step_traces (
			run_id VARCHAR,
			step_id VARCHAR,
			status VARCHAR,
			input_summary VARCHAR,
			output_summary VARCHAR,
			artifact_refs JSON,
			tokens BIGINT,
			error VARCHAR,
			fallback_used BOOLEAN
		)`,
	}
	for _, s := range stmts {
		if _, err := d.db.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func (d *DuckDB) Close() error { return d.db.Close() }

func sanitize(id string) string {
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

func duckType(t core.FieldType) string {
	switch t {
	case core.FieldInt:
		return "BIGINT"
	case core.FieldFloat:
		return "DOUBLE"
	case core.FieldBool:
		return "BOOLEAN"
	case core.FieldTimestamp:
		return "TIMESTAMP"
	default:
		return "VARCHAR"
	}
}

func (d *DuckDB) tableName(pipelineID string) string { return "data_" + sanitize(pipelineID) }

func (d *DuckDB) EnsureTable(pipelineID string, fields []core.Field) error {
	cols := make([]string, 0, len(fields)+2)
	for _, f := range fields {
		cols = append(cols, fmt.Sprintf("%s %s", sanitize(f.Name), duckType(f.Type)))
	}
	cols = append(cols, "__run_id VARCHAR", "__fetched_at TIMESTAMP")
	stmt := fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (%s)", d.tableName(pipelineID), strings.Join(cols, ", "))
	_, err := d.db.Exec(stmt)
	return err
}

func (d *DuckDB) AppendRows(pipelineID string, fields []core.Field, runID string, rows []map[string]any) error {
	if len(rows) == 0 {
		return nil
	}
	colNames := make([]string, 0, len(fields)+2)
	for _, f := range fields {
		colNames = append(colNames, sanitize(f.Name))
	}
	colNames = append(colNames, "__run_id", "__fetched_at")

	placeholders := make([]string, len(colNames))
	for i := range placeholders {
		placeholders[i] = "?"
	}
	// __fetched_at uses now() so the last placeholder is dropped for it.
	insertCols := strings.Join(colNames[:len(colNames)-1], ", ")
	insertPH := strings.Join(placeholders[:len(placeholders)-1], ", ")
	stmt := fmt.Sprintf("INSERT INTO %s (%s, __fetched_at) VALUES (%s, now())",
		d.tableName(pipelineID), insertCols, insertPH)

	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	prepared, err := tx.Prepare(stmt)
	if err != nil {
		return err
	}
	for _, row := range rows {
		args := make([]any, 0, len(fields)+1)
		for _, f := range fields {
			args = append(args, row[f.Name])
		}
		args = append(args, runID)
		if _, err := prepared.Exec(args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (d *DuckDB) Query(query string) ([]map[string]any, error) {
	rows, err := d.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		m := make(map[string]any, len(cols))
		for i, c := range cols {
			m[c] = vals[i]
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (d *DuckDB) RecordRun(r core.Run) error {
	inputJSON, _ := json.Marshal(r.Input)
	_, err := d.db.Exec(
		`INSERT INTO runs (id, pipeline_id, input, status, started_at, finished_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT (id) DO UPDATE SET status = excluded.status, finished_at = excluded.finished_at`,
		r.ID, r.PipelineID, string(inputJSON), string(r.Status), r.StartedAt, r.FinishedAt,
	)
	return err
}

func (d *DuckDB) RecordTrace(t core.StepTrace) error {
	refsJSON, _ := json.Marshal(t.ArtifactRefs)
	_, err := d.db.Exec(
		`INSERT INTO step_traces (run_id, step_id, status, input_summary, output_summary, artifact_refs, tokens, error, fallback_used)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.RunID, t.StepID, t.Status, t.InputSummary, t.OutputSummary, string(refsJSON), t.Tokens, t.Error, t.FallbackUsed,
	)
	return err
}
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `CGO_ENABLED=1 go test ./internal/providers/store/ && go vet ./internal/providers/store/`
Expected: PASS (the cgo DuckDB integration works end to end), vet clean.

- [ ] **Step 7: Run the full suite and commit**

Run: `go test ./... && go vet ./...`
Expected: all packages PASS.

```bash
go mod tidy
git add internal/providers/store/ go.mod go.sum
git commit -m "feat: DuckDB row store with results tables and run/trace history"
```

---

## Foundation Definition of Done

- `go build ./...` and `go test ./...` pass with `CGO_ENABLED=1` (the default).
- `go vet ./...` is clean and files are `gofmt`'d.
- These packages exist and are tested with fakes/temp resources, no network: `internal/core`, `internal/config`, `internal/agent`, `internal/providers/{search,fetch,artifacts,store}`.
- The DuckDB cgo dependency compiles and round-trips data, confirming the toolchain for the rest of v1.

## Self-Review Notes

- **Spec coverage (this plan's slice):** core types ✓ (Task 1), role→model config ✓ (Task 2), Ollama structured-output client ✓ (Task 3), pluggable Tavily search ✓ (Task 4), HTTP fetch + readability + browser stub via `ErrMethodUnavailable` ✓ (Task 5), on-disk artifact store ✓ (Task 6), DuckDB results + run/trace history behind a `Store` interface ✓ (Task 7). Engine, agent flows, and TUI are intentionally deferred to Plans 2–4.
- **Type consistency:** `core.Field`/`core.FieldType` are defined once (Task 1) and consumed by the store (Task 7); the store interface uses the exact `AppendRows(pipelineID, fields, runID, rows)` signature in both `store.go` and `duckdb.go` and the test. `LLM`, `Search`, `Fetcher`, `artifacts.Store`, `store.Store` are the interface names later plans will depend on.
- **No placeholders:** every step includes complete, compilable code and exact commands.
