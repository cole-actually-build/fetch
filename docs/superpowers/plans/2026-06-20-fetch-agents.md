# Fetch Agents Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the LLM-backed agent layer — a conversational pipeline **builder** (Interviewer → SchemaDesigner → Planner with a validate-repair loop) and a run-time **Replanner** that fills the engine's existing fallback seam — plus a `fetch create` CLI, all headless and fake-testable.

**Architecture:** A new `internal/builder` package owns create-time intelligence: each agent role is one structured `LLM.Chat` call (Ollama JSON-schema `Format`), and a `CreateSession` orchestrates interview → split design → bounded validate-repair → conversational redraft → accept (ensure table + save JSON). A new `internal/replanner` package implements `engine.Replanner` (run-time step repair). The `internal/cli` package gains a `create` command and wires the real Replanner into `run`.

**Tech Stack:** Go 1.26, stdlib only (`encoding/json`, `bufio`, `regexp`, `strings`, `context`, `time`), building on Plan 1–2 packages (`internal/core`, `internal/config`, `internal/agent`, `internal/pipeline`, `internal/engine`, `internal/providers/*`).

This is **Plan 3 of 4** (Foundation ✓ → Engine ✓ → Agents → TUI). Spec: `docs/superpowers/specs/2026-06-20-fetch-agents-design.md`.

## Global Constraints

- Module path `github.com/cole/fetch`; Go floor `go 1.26`; `CGO_ENABLED=1` for all `go` commands (the `store`/`cli` packages pull DuckDB via cgo; clang is installed).
- Build on existing interfaces verbatim — do NOT change Plan 1–2 signatures:
  - `agent.LLM.Chat(ctx, agent.ChatRequest) (agent.ChatResponse, error)`; `ChatRequest{Model string; Messages []agent.Message; Format json.RawMessage; Temperature float64}`; `agent.Message{Role, Content string}`; `agent.ChatResponse{Content string; PromptTokens, CompletionTokens int}`; `agent.NewOllama(baseURL string, hc *http.Client) *agent.Ollama`; test fake `agent.FakeLLM{Responses []agent.ChatResponse; Err error; Calls []agent.ChatRequest}`.
  - `config.Config.ModelFor(role string) string`; role consts `config.RoleInterview`, `config.RoleSchema`, `config.RolePlan`, `config.RoleReplan` (all exist).
  - `pipeline.Validate(core.Pipeline) error`; `pipeline.TopoOrder(core.Pipeline) ([]core.Step, error)`; `pipeline.NewRepository(dataDir string) *pipeline.Repository` with `Save(core.Pipeline) error`, `Load(id string) (core.Pipeline, error)`.
  - `store.Store.EnsureTable(ctx, pipelineID string, fields []core.Field) error`; test fake `store.NewFakeStore()`; real `store.OpenDuckDB(path string) (*store.DuckDB, error)`.
  - `engine.Replanner interface { Replan(ctx, engine.ReplanRequest) (engine.Decision, error) }`; `engine.Decision{Action string; Step core.Step; Reason string}`; consts `engine.ActionAdapt`, `engine.ActionSkip`, `engine.ActionAbort`; `engine.ReplanRequest{Pipeline core.Pipeline; Step core.Step; Params map[string]any; Attempt int; Err string}`; `engine.New(engine.Deps) *engine.Engine` with `Deps.Replanner engine.Replanner`.
- Domain types come from `internal/core` ONLY — never redefine `Pipeline`/`Step`/`Field`/`InputParam`. `core.Field` JSON tags: `name`,`type`,`description`. `core.InputParam`: `name`,`type`,`required`,`description`. `core.Step`: `id`,`name`,`type`,`params`,`depends_on`. Field types: `string`,`int`,`float`,`bool`,`timestamp`.
- Every agent call sets `Format` to a JSON schema (Ollama structured output) and uses `cfg.ModelFor(<role>)` for the model.
- No real network in `go test ./...` — all agent tests use `agent.FakeLLM`; store via `store.NewFakeStore()`; repo via `t.TempDir()`.
- Determinism: `CreateSession` takes injectable `Now func() time.Time` and `IDGen func() string`; tests inject fixed ones.
- `gofmt -l internal/ cmd/` empty and `go vet ./...` clean before each commit.

### Shared agent-call shape (every executor task follows this)

An agent method renders a system prompt + a user prompt, calls `LLM.Chat` with a JSON-schema `Format`, and `json.Unmarshal`s `resp.Content` into a typed struct whose JSON tags match the schema. JSON-schema builders return `json.RawMessage` (marshal a `map[string]any`, mirroring `engine.buildExtractSchema`).

---

### Task 1: Builder types, prompts, and the Interviewer

**Files:**
- Create: `internal/builder/types.go`
- Create: `internal/builder/prompts.go`
- Create: `internal/builder/interviewer.go`
- Create: `internal/builder/interviewer_test.go`

**Interfaces:**
- Consumes: `core.Field`, `core.InputParam`; `agent.LLM`, `agent.ChatRequest`, `agent.Message`; `config.Config`, `config.RoleInterview`.
- Produces:
  - `type Turn struct { Role, Content string }`
  - `type Facts struct { Domain string; Inputs []core.InputParam; OutputFields []core.Field; SourceHints []string }`
  - `type InterviewState struct { Goal string; Transcript []Turn; Facts Facts; Done bool }`
  - `type Interviewer struct{...}`; `func NewInterviewer(llm agent.LLM, cfg config.Config) *Interviewer`
  - `func (iv *Interviewer) Next(ctx context.Context, st InterviewState) (question string, ready bool, facts Facts, err error)`
  - prompt/schema helpers (`fieldSchema`, `inputParamSchema`, `factsSchema`, `interviewSystemPrompt`, `interviewUserPrompt`, `renderTranscript`, `fieldTypeEnum`).

- [ ] **Step 1: Write the failing test**

Create `internal/builder/interviewer_test.go`:

```go
package builder

import (
	"context"
	"strings"
	"testing"

	"github.com/cole/fetch/internal/agent"
	"github.com/cole/fetch/internal/config"
	"github.com/cole/fetch/internal/core"
)

func TestInterviewerAsksThenReady(t *testing.T) {
	llm := &agent.FakeLLM{Responses: []agent.ChatResponse{
		{Content: `{"question":"What part number format do you use?","ready":false,"facts":{"domain":"truck-parts","inputs":[],"output_fields":[],"source_hints":[]}}`},
		{Content: `{"question":"","ready":true,"facts":{"domain":"truck-parts","inputs":[{"name":"part","type":"string","required":true,"description":"part number"}],"output_fields":[{"name":"cross_ref","type":"string","description":"cross reference"}],"source_hints":["rockauto.com"]}}`},
	}}
	iv := NewInterviewer(llm, config.Default())

	q, ready, facts, err := iv.Next(context.Background(), InterviewState{Goal: "truck part cross references"})
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if ready || q == "" {
		t.Fatalf("first turn should ask a question: q=%q ready=%v", q, ready)
	}
	if facts.Domain != "truck-parts" {
		t.Fatalf("facts.Domain = %q", facts.Domain)
	}

	q2, ready2, facts2, err := iv.Next(context.Background(), InterviewState{
		Goal:       "truck part cross references",
		Transcript: []Turn{{Role: "assistant", Content: q}, {Role: "user", Content: "ABC-123 style"}},
	})
	if err != nil {
		t.Fatalf("next2: %v", err)
	}
	if !ready2 || q2 != "" {
		t.Fatalf("second turn should be ready: q=%q ready=%v", q2, ready2)
	}
	if len(facts2.Inputs) != 1 || facts2.Inputs[0].Name != "part" || len(facts2.OutputFields) != 1 {
		t.Fatalf("facts2 not parsed: %+v", facts2)
	}
	// the call used structured output (Format) and the interview model.
	last := llm.Calls[len(llm.Calls)-1]
	if last.Format == nil {
		t.Fatal("expected Format (JSON schema) on the interview call")
	}
	if last.Model != config.Default().ModelFor(config.RoleInterview) {
		t.Fatalf("model = %q", last.Model)
	}
}

func TestInterviewSystemPromptOneAtATime(t *testing.T) {
	sys := interviewSystemPrompt()
	if !strings.Contains(sys, "ONE") {
		t.Fatalf("system prompt must instruct one question at a time:\n%s", sys)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=1 go test ./internal/builder/`
Expected: FAIL — `undefined: NewInterviewer`, `undefined: InterviewState`.

- [ ] **Step 3: Write `internal/builder/types.go`**

```go
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
	Domain       string             `json:"domain"`
	Inputs       []core.InputParam  `json:"inputs"`
	OutputFields []core.Field       `json:"output_fields"`
	SourceHints  []string           `json:"source_hints"`
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
```

- [ ] **Step 4: Write `internal/builder/prompts.go`**

```go
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
```

- [ ] **Step 5: Write `internal/builder/interviewer.go`**

```go
package builder

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cole/fetch/internal/agent"
	"github.com/cole/fetch/internal/config"
)

// Interviewer drives the one-question-at-a-time interview via the LLM.
type Interviewer struct {
	llm agent.LLM
	cfg config.Config
}

// NewInterviewer builds an Interviewer.
func NewInterviewer(llm agent.LLM, cfg config.Config) *Interviewer {
	return &Interviewer{llm: llm, cfg: cfg}
}

type interviewReply struct {
	Question string `json:"question"`
	Ready    bool   `json:"ready"`
	Facts    Facts  `json:"facts"`
}

// Next runs one interview turn: it returns the next question (empty when ready),
// whether the interview is complete, and the model's current understanding.
func (iv *Interviewer) Next(ctx context.Context, st InterviewState) (string, bool, Facts, error) {
	resp, err := iv.llm.Chat(ctx, agent.ChatRequest{
		Model: iv.cfg.ModelFor(config.RoleInterview),
		Messages: []agent.Message{
			{Role: "system", Content: interviewSystemPrompt()},
			{Role: "user", Content: interviewUserPrompt(st)},
		},
		Format: interviewReplySchema(),
	})
	if err != nil {
		return "", false, Facts{}, err
	}
	var r interviewReply
	if err := json.Unmarshal([]byte(resp.Content), &r); err != nil {
		return "", false, Facts{}, fmt.Errorf("interviewer: decode reply: %w", err)
	}
	return r.Question, r.Ready, r.Facts, nil
}
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `CGO_ENABLED=1 go test ./internal/builder/ && CGO_ENABLED=1 go vet ./internal/builder/ && gofmt -l internal/builder/`
Expected: PASS, vet clean, gofmt empty.

- [ ] **Step 7: Commit** (skip if the run is configured for end-of-run commits)

```bash
git add internal/builder/types.go internal/builder/prompts.go internal/builder/interviewer.go internal/builder/interviewer_test.go
git commit -m "feat: builder types, prompts, and interviewer agent"
```

---

### Task 2: SchemaDesigner

**Files:**
- Create: `internal/builder/designer.go`
- Create: `internal/builder/designer_test.go`

**Interfaces:**
- Consumes: Task 1 (`Facts`, prompt helpers, `fieldSchema`/`inputParamSchema`); `agent.LLM`, `config.RoleSchema`; `core.InputParam`, `core.Field`.
- Produces:
  - `type designOutput struct { Name, Description, Domain string; Inputs []core.InputParam; Schema []core.Field }`
  - `type SchemaDesigner struct{...}`; `func NewSchemaDesigner(llm agent.LLM, cfg config.Config) *SchemaDesigner`
  - `func (d *SchemaDesigner) Design(ctx context.Context, goal string, facts Facts, feedback string) (designOutput, error)`

- [ ] **Step 1: Write the failing test**

Create `internal/builder/designer_test.go`:

```go
package builder

import (
	"context"
	"testing"

	"github.com/cole/fetch/internal/agent"
	"github.com/cole/fetch/internal/config"
	"github.com/cole/fetch/internal/core"
)

func TestSchemaDesignerProducesSchema(t *testing.T) {
	llm := &agent.FakeLLM{Responses: []agent.ChatResponse{{
		Content: `{"name":"Truck Cross Ref","description":"cross references","domain":"truck-parts",
		"inputs":[{"name":"part","type":"string","required":true,"description":"part number"}],
		"schema":[{"name":"part","type":"string","description":"the part"},{"name":"cross_ref","type":"string","description":"x-ref"}]}`,
	}}}
	d := NewSchemaDesigner(llm, config.Default())
	out, err := d.Design(context.Background(), "truck cross refs", Facts{Domain: "truck-parts"}, "")
	if err != nil {
		t.Fatalf("design: %v", err)
	}
	if out.Name != "Truck Cross Ref" || out.Domain != "truck-parts" {
		t.Fatalf("out = %+v", out)
	}
	if len(out.Inputs) != 1 || out.Inputs[0].Type != core.FieldString {
		t.Fatalf("inputs = %+v", out.Inputs)
	}
	if len(out.Schema) != 2 || out.Schema[1].Name != "cross_ref" {
		t.Fatalf("schema = %+v", out.Schema)
	}
	last := llm.Calls[len(llm.Calls)-1]
	if last.Format == nil {
		t.Fatal("expected Format on schema call")
	}
	if last.Model != config.Default().ModelFor(config.RoleSchema) {
		t.Fatalf("model = %q", last.Model)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=1 go test ./internal/builder/ -run SchemaDesigner`
Expected: FAIL — `undefined: NewSchemaDesigner`.

- [ ] **Step 3: Write `internal/builder/designer.go`**

```go
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `CGO_ENABLED=1 go test ./internal/builder/ && CGO_ENABLED=1 go vet ./internal/builder/ && gofmt -l internal/builder/`
Expected: PASS, vet clean, gofmt empty.

- [ ] **Step 5: Commit**

```bash
git add internal/builder/designer.go internal/builder/designer_test.go
git commit -m "feat: schema designer agent"
```

---

### Task 3: Planner with bounded validate-repair

**Files:**
- Create: `internal/builder/planner.go`
- Create: `internal/builder/planner_test.go`

**Interfaces:**
- Consumes: Task 1 helpers; `agent.LLM`, `config.RolePlan`; `core.Pipeline`, `core.Step`; `pipeline.Validate`.
- Produces:
  - `type Planner struct{...}`; `func NewPlanner(llm agent.LLM, cfg config.Config) *Planner`
  - `func (p *Planner) Plan(ctx context.Context, base core.Pipeline, facts Facts, feedback string) ([]core.Step, error)` — one call.
  - `func (p *Planner) PlanWithRepair(ctx context.Context, base core.Pipeline, facts Facts, feedback string, maxRepairs int) (core.Pipeline, error)` — fills `base.Plan`, validates, and re-invokes the Planner with the validation error appended up to `maxRepairs` times.

- [ ] **Step 1: Write the failing test**

Create `internal/builder/planner_test.go`:

```go
package builder

import (
	"context"
	"testing"

	"github.com/cole/fetch/internal/agent"
	"github.com/cole/fetch/internal/config"
	"github.com/cole/fetch/internal/core"
)

func base() core.Pipeline {
	return core.Pipeline{
		ID:     "p",
		Name:   "P",
		Inputs: []core.InputParam{{Name: "q", Type: core.FieldString}},
		Schema: []core.Field{{Name: "title", Type: core.FieldString}},
	}
}

const validPlanJSON = `{"plan":[
 {"id":"search","name":"search","type":"search","params":{"query":"{{input.q}}"},"depends_on":[]},
 {"id":"store","name":"store","type":"store","params":{"rows":"{{steps.search.results}}"},"depends_on":["search"]}
]}`

// references an unknown dep -> pipeline.Validate fails -> triggers a repair.
const invalidPlanJSON = `{"plan":[
 {"id":"store","name":"store","type":"store","params":{},"depends_on":["ghost"]}
]}`

func TestPlannerSingleCall(t *testing.T) {
	llm := &agent.FakeLLM{Responses: []agent.ChatResponse{{Content: validPlanJSON}}}
	p := NewPlanner(llm, config.Default())
	steps, err := p.Plan(context.Background(), base(), Facts{}, "")
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(steps) != 2 || steps[0].ID != "search" || steps[1].Type != core.StepStore {
		t.Fatalf("steps = %+v", steps)
	}
	if llm.Calls[len(llm.Calls)-1].Model != config.Default().ModelFor(config.RolePlan) {
		t.Fatalf("model = %q", llm.Calls[0].Model)
	}
}

func TestPlanWithRepairFixesInvalidPlan(t *testing.T) {
	llm := &agent.FakeLLM{Responses: []agent.ChatResponse{
		{Content: invalidPlanJSON}, // first attempt: invalid
		{Content: validPlanJSON},   // repair: valid
	}}
	p := NewPlanner(llm, config.Default())
	full, err := p.PlanWithRepair(context.Background(), base(), Facts{}, "", 2)
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if len(full.Plan) != 2 {
		t.Fatalf("plan = %+v", full.Plan)
	}
	if len(llm.Calls) != 2 {
		t.Fatalf("expected 2 calls (initial + 1 repair), got %d", len(llm.Calls))
	}
}

func TestPlanWithRepairGivesUp(t *testing.T) {
	llm := &agent.FakeLLM{Responses: []agent.ChatResponse{{Content: invalidPlanJSON}}}
	p := NewPlanner(llm, config.Default())
	if _, err := p.PlanWithRepair(context.Background(), base(), Facts{}, "", 1); err == nil {
		t.Fatal("expected error after exhausting repairs")
	}
	// 1 initial + 1 repair = 2 attempts.
	if len(llm.Calls) != 2 {
		t.Fatalf("calls = %d", len(llm.Calls))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=1 go test ./internal/builder/ -run Plan`
Expected: FAIL — `undefined: NewPlanner`.

- [ ] **Step 3: Write `internal/builder/planner.go`**

```go
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
```

Note: `pipeline.Validate` requires a non-empty `ID` and at least one step; `base` carries the ID the session assigns before planning (the test sets `ID:"p"`). The session (Task 4) sets a temporary ID on `base` before calling `PlanWithRepair`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `CGO_ENABLED=1 go test ./internal/builder/ && CGO_ENABLED=1 go vet ./internal/builder/ && gofmt -l internal/builder/`
Expected: PASS, vet clean, gofmt empty.

- [ ] **Step 5: Commit**

```bash
git add internal/builder/planner.go internal/builder/planner_test.go
git commit -m "feat: planner agent with bounded validate-repair loop"
```

---

### Task 4: CreateSession orchestration

**Files:**
- Create: `internal/builder/session.go`
- Create: `internal/builder/session_test.go`

**Interfaces:**
- Consumes: Tasks 1–3 (`Interviewer`, `SchemaDesigner`, `Planner`, `Facts`, `Draft`, `designOutput`); `agent.LLM`; `config.Config`; `store.Store`; `pipeline.Repository`; `core.Pipeline`.
- Produces:
  - `type SessionDeps struct { LLM agent.LLM; Cfg config.Config; Store store.Store; Repo *pipeline.Repository; Now func() time.Time; IDGen func() string; MaxRepairs int }`
  - `type CreateSession struct{...}`; `func NewSession(d SessionDeps) *CreateSession`
  - `func (s *CreateSession) Start(goal string)`
  - `func (s *CreateSession) Reply(ctx context.Context, msg string) (question string, ready bool, err error)`
  - `func (s *CreateSession) Finalize(ctx context.Context) (Draft, error)`
  - `func (s *CreateSession) Redraft(ctx context.Context, comment string) (Draft, error)`
  - `func (s *CreateSession) Accept(ctx context.Context, d Draft) (id string, err error)`
  - `func slugify(string) string`

- [ ] **Step 1: Write the failing test**

Create `internal/builder/session_test.go`:

```go
package builder

import (
	"context"
	"testing"
	"time"

	"github.com/cole/fetch/internal/agent"
	"github.com/cole/fetch/internal/config"
	"github.com/cole/fetch/internal/pipeline"
	"github.com/cole/fetch/internal/providers/store"
)

func fixedSessionDeps(t *testing.T, llm agent.LLM) SessionDeps {
	t.Helper()
	return SessionDeps{
		LLM:   llm,
		Cfg:   config.Default(),
		Store: store.NewFakeStore(),
		Repo:  pipeline.NewRepository(t.TempDir()),
		Now:   func() time.Time { return time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC) },
		IDGen: func() string { return "x1" },
	}
}

// scripted responses for: interview(ready) -> design -> plan
func scriptedLLM() *agent.FakeLLM {
	return &agent.FakeLLM{Responses: []agent.ChatResponse{
		{Content: `{"question":"","ready":true,"facts":{"domain":"d","inputs":[],"output_fields":[],"source_hints":[]}}`},
		{Content: `{"name":"My Pipeline","description":"x","domain":"d","inputs":[{"name":"q","type":"string","required":true,"description":"q"}],"schema":[{"name":"title","type":"string","description":"t"}]}`},
		{Content: validPlanJSON},
	}}
}

func TestSessionCreateAndAccept(t *testing.T) {
	deps := fixedSessionDeps(t, scriptedLLM())
	fs := deps.Store.(*store.FakeStore)
	s := NewSession(deps)
	s.Start("build me a thing")

	q, ready, err := s.Reply(context.Background(), "")
	if err != nil {
		t.Fatalf("reply: %v", err)
	}
	if !ready || q != "" {
		t.Fatalf("expected ready: q=%q ready=%v", q, ready)
	}
	draft, err := s.Finalize(context.Background())
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if draft.Pipeline.Name != "My Pipeline" || len(draft.Pipeline.Plan) != 2 {
		t.Fatalf("draft = %+v", draft.Pipeline)
	}
	id, err := s.Accept(context.Background(), draft)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if id != "my-pipeline" {
		t.Fatalf("id = %q", id)
	}
	if _, ok := fs.Tables[id]; !ok {
		t.Fatalf("table not ensured: %v", fs.Tables)
	}
	saved, err := deps.Repo.Load(id)
	if err != nil {
		t.Fatalf("load saved: %v", err)
	}
	if saved.Version != 1 || saved.CreatedAt.IsZero() {
		t.Fatalf("saved meta = %+v", saved)
	}
}

func TestSessionRedraft(t *testing.T) {
	llm := scriptedLLM()
	// add a second design+plan pair for the redraft.
	llm.Responses = append(llm.Responses,
		agent.ChatResponse{Content: `{"name":"Revised","description":"x","domain":"d","inputs":[{"name":"q","type":"string","required":true,"description":"q"}],"schema":[{"name":"title","type":"string","description":"t"}]}`},
		agent.ChatResponse{Content: validPlanJSON},
	)
	s := NewSession(fixedSessionDeps(t, llm))
	s.Start("thing")
	if _, _, err := s.Reply(context.Background(), ""); err != nil {
		t.Fatalf("reply: %v", err)
	}
	if _, err := s.Finalize(context.Background()); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	d2, err := s.Redraft(context.Background(), "rename it")
	if err != nil {
		t.Fatalf("redraft: %v", err)
	}
	if d2.Pipeline.Name != "Revised" {
		t.Fatalf("redraft name = %q", d2.Pipeline.Name)
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{"Truck Cross Ref": "truck-cross-ref", "  A/B  ": "a-b", "": "pipeline"}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Fatalf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=1 go test ./internal/builder/ -run Session`
Expected: FAIL — `undefined: NewSession`.

- [ ] **Step 3: Write `internal/builder/session.go`**

```go
package builder

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/cole/fetch/internal/agent"
	"github.com/cole/fetch/internal/config"
	"github.com/cole/fetch/internal/core"
	"github.com/cole/fetch/internal/pipeline"
	"github.com/cole/fetch/internal/providers/store"
)

// SessionDeps are the CreateSession's collaborators and tunables.
type SessionDeps struct {
	LLM        agent.LLM
	Cfg        config.Config
	Store      store.Store
	Repo       *pipeline.Repository
	Now        func() time.Time
	IDGen      func() string
	MaxRepairs int
}

// CreateSession orchestrates interview -> design -> plan(repair) -> accept.
type CreateSession struct {
	deps      SessionDeps
	iv        *Interviewer
	designer  *SchemaDesigner
	planner   *Planner
	state     InterviewState
	lastDraft Draft
}

// NewSession builds a CreateSession, filling defaults.
func NewSession(d SessionDeps) *CreateSession {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.IDGen == nil {
		d.IDGen = defaultIDGen()
	}
	if d.MaxRepairs == 0 {
		d.MaxRepairs = 2
	}
	return &CreateSession{
		deps:     d,
		iv:       NewInterviewer(d.LLM, d.Cfg),
		designer: NewSchemaDesigner(d.LLM, d.Cfg),
		planner:  NewPlanner(d.LLM, d.Cfg),
	}
}

func defaultIDGen() func() string {
	var mu sync.Mutex
	var n int
	return func() string {
		mu.Lock()
		n++
		id := n
		mu.Unlock()
		return fmt.Sprintf("%d-%d", time.Now().Unix(), id)
	}
}

// Start sets the user's opening goal.
func (s *CreateSession) Start(goal string) {
	s.state = InterviewState{Goal: goal}
}

// Reply feeds an optional user message and runs one interview turn.
func (s *CreateSession) Reply(ctx context.Context, msg string) (string, bool, error) {
	if msg != "" {
		s.state.Transcript = append(s.state.Transcript, Turn{Role: "user", Content: msg})
	}
	q, ready, facts, err := s.iv.Next(ctx, s.state)
	if err != nil {
		return "", false, err
	}
	s.state.Facts = facts
	if q != "" {
		s.state.Transcript = append(s.state.Transcript, Turn{Role: "assistant", Content: q})
	}
	s.state.Done = ready
	return q, ready, nil
}

// Finalize produces the first draft from the gathered facts.
func (s *CreateSession) Finalize(ctx context.Context) (Draft, error) {
	return s.design(ctx, "")
}

// Redraft revises the last draft per a natural-language comment.
func (s *CreateSession) Redraft(ctx context.Context, comment string) (Draft, error) {
	prior, _ := json.Marshal(s.lastDraft.Pipeline)
	fb := fmt.Sprintf("Revise the previous draft per this feedback: %s\nPrevious draft: %s", comment, prior)
	return s.design(ctx, fb)
}

func (s *CreateSession) design(ctx context.Context, feedback string) (Draft, error) {
	out, err := s.designer.Design(ctx, s.state.Goal, s.state.Facts, feedback)
	if err != nil {
		return Draft{}, err
	}
	base := core.Pipeline{
		ID:          "draft", // temporary, satisfies pipeline.Validate; replaced on Accept
		Name:        out.Name,
		Description: out.Description,
		Domain:      out.Domain,
		Inputs:      out.Inputs,
		Schema:      out.Schema,
	}
	full, err := s.planner.PlanWithRepair(ctx, base, s.state.Facts, feedback, s.deps.MaxRepairs)
	if err != nil {
		return Draft{}, err
	}
	d := Draft{Pipeline: full}
	s.lastDraft = d
	return d, nil
}

// Accept assigns an id, ensures the results table, and saves the pipeline JSON.
func (s *CreateSession) Accept(ctx context.Context, d Draft) (string, error) {
	p := d.Pipeline
	p.ID = s.assignID(p.Name)
	p.Version = 1
	p.CreatedAt = s.deps.Now()
	if err := s.deps.Store.EnsureTable(ctx, p.ID, p.Schema); err != nil {
		return "", fmt.Errorf("ensure table: %w", err)
	}
	if err := s.deps.Repo.Save(p); err != nil {
		return "", fmt.Errorf("save pipeline: %w", err)
	}
	return p.ID, nil
}

func (s *CreateSession) assignID(name string) string {
	id := slugify(name)
	if _, err := s.deps.Repo.Load(id); err == nil {
		id = id + "-" + s.deps.IDGen()
	}
	return id
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(name string) string {
	s := nonSlug.ReplaceAllString(strings.ToLower(name), "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "pipeline"
	}
	return s
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `CGO_ENABLED=1 go test ./internal/builder/ && CGO_ENABLED=1 go vet ./internal/builder/ && gofmt -l internal/builder/`
Expected: PASS (all builder tests), vet clean, gofmt empty.

- [ ] **Step 5: Commit**

```bash
git add internal/builder/session.go internal/builder/session_test.go
git commit -m "feat: create session orchestrating interview, design, plan, accept"
```

---

### Task 5: LLM-backed Replanner

**Files:**
- Create: `internal/replanner/replanner.go`
- Create: `internal/replanner/replanner_test.go`

**Interfaces:**
- Consumes: `agent.LLM`, `config.RoleReplan`; `engine.Replanner`, `engine.ReplanRequest`, `engine.Decision`, `engine.Action*`; `core.Step`. For the integration test: `engine.New`, `engine.Deps`, the foundation fakes.
- Produces:
  - `type Replanner struct{...}`; `func New(llm agent.LLM, cfg config.Config) *Replanner`
  - `func (r *Replanner) Replan(ctx context.Context, req engine.ReplanRequest) (engine.Decision, error)` (satisfies `engine.Replanner`).

- [ ] **Step 1: Write the failing test**

Create `internal/replanner/replanner_test.go`:

```go
package replanner

import (
	"context"
	"testing"
	"time"

	"github.com/cole/fetch/internal/agent"
	"github.com/cole/fetch/internal/config"
	"github.com/cole/fetch/internal/core"
	"github.com/cole/fetch/internal/engine"
	"github.com/cole/fetch/internal/providers/artifacts"
	"github.com/cole/fetch/internal/providers/search"
	"github.com/cole/fetch/internal/providers/store"
)

var _ engine.Replanner = (*Replanner)(nil)

func req() engine.ReplanRequest {
	return engine.ReplanRequest{
		Step:    core.Step{ID: "search", Type: core.StepSearch, Params: map[string]any{"query": "bad"}},
		Params:  map[string]any{"query": "bad"},
		Attempt: 1,
		Err:     "search: no results",
	}
}

func TestReplanAdaptForcesSameIDAndType(t *testing.T) {
	llm := &agent.FakeLLM{Responses: []agent.ChatResponse{{
		Content: `{"action":"adapt","reason":"broaden","step":{"id":"WRONG","name":"s","type":"store","params":{"query":"good"},"depends_on":[]}}`,
	}}}
	d, err := New(llm, config.Default()).Replan(context.Background(), req())
	if err != nil {
		t.Fatalf("replan: %v", err)
	}
	if d.Action != engine.ActionAdapt {
		t.Fatalf("action = %q", d.Action)
	}
	if d.Step.ID != "search" || d.Step.Type != core.StepSearch {
		t.Fatalf("adapt must keep original id/type: %+v", d.Step)
	}
	if d.Step.Params["query"] != "good" {
		t.Fatalf("params = %+v", d.Step.Params)
	}
}

func TestReplanSkipAndAbort(t *testing.T) {
	for _, tc := range []struct {
		content string
		want    string
	}{
		{`{"action":"skip","reason":"optional"}`, engine.ActionSkip},
		{`{"action":"abort","reason":"dead"}`, engine.ActionAbort},
		{`not json`, engine.ActionAbort}, // malformed -> abort, no error
	} {
		llm := &agent.FakeLLM{Responses: []agent.ChatResponse{{Content: tc.content}}}
		d, err := New(llm, config.Default()).Replan(context.Background(), req())
		if err != nil {
			t.Fatalf("replan(%q): %v", tc.content, err)
		}
		if d.Action != tc.want {
			t.Fatalf("content %q -> action %q, want %q", tc.content, d.Action, tc.want)
		}
	}
}

// queryAwareSearch fails until the query becomes "good".
type queryAwareSearch struct{ calls []string }

func (q *queryAwareSearch) Search(_ context.Context, query string, _ search.Options) ([]search.Result, error) {
	q.calls = append(q.calls, query)
	if query != "good" {
		return nil, nil
	}
	return []search.Result{{Title: "T", URL: "https://x", Content: "body"}}, nil
}

func TestReplannerDrivesEngineSelfHeal(t *testing.T) {
	llm := &agent.FakeLLM{Responses: []agent.ChatResponse{{
		Content: `{"action":"adapt","reason":"broaden","step":{"id":"search","name":"search","type":"search","params":{"query":"good"},"depends_on":[]}}`,
	}}}
	e := engine.New(engine.Deps{
		Config:    config.Default(),
		Search:    &queryAwareSearch{},
		Store:     store.NewFakeStore(),
		Artifacts: artifacts.NewFakeArtifacts(),
		Replanner: New(llm, config.Default()),
		MaxRetries: 2,
		Now:       func() time.Time { return time.Unix(0, 0) },
		IDGen:     func() string { return "run-1" },
	})
	p := core.Pipeline{ID: "p", Plan: []core.Step{
		{ID: "search", Type: core.StepSearch, Params: map[string]any{"query": "bad"}},
	}}
	res, err := e.Run(context.Background(), p, nil, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Run.Status != core.RunOK {
		t.Fatalf("status = %s", res.Run.Status)
	}
	if len(res.Candidates) != 1 || res.Candidates[0].Adapted.Params["query"] != "good" {
		t.Fatalf("expected self-heal candidate: %+v", res.Candidates)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=1 go test ./internal/replanner/`
Expected: FAIL — `undefined: New`.

- [ ] **Step 3: Write `internal/replanner/replanner.go`**

```go
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `CGO_ENABLED=1 go test ./internal/replanner/ && CGO_ENABLED=1 go vet ./internal/replanner/ && gofmt -l internal/replanner/`
Expected: PASS, vet clean, gofmt empty.

- [ ] **Step 5: Commit**

```bash
git add internal/replanner/replanner.go internal/replanner/replanner_test.go
git commit -m "feat: LLM-backed replanner wired to the engine fallback seam"
```

---

### Task 6: `fetch create` CLI + Replanner wiring

**Files:**
- Create: `internal/cli/create.go`
- Modify: `internal/cli/cli.go` (add `create` dispatch + stdin param; wire Replanner into `run`; extend usage)
- Modify: `internal/cli/cli_test.go` (update `Run` call sites for the new stdin arg; add create-loop tests)
- Modify: `cmd/fetch/main.go` (pass `os.Stdin`)

**Interfaces:**
- Consumes: `internal/builder` (`NewSession`, `SessionDeps`, `Draft`, `CreateSession`); `internal/replanner` (`New`); existing CLI helpers (`config.Load`, `defaultConfigPath`, `defaultDBPath`, `store.OpenDuckDB`, `pipeline.NewRepository`, `agent.NewOllama`).
- Produces:
  - `func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int` (signature changes — add `stdin`).
  - `func createPipeline(args []string, stdin io.Reader, stdout, stderr io.Writer) int`
  - `type creator interface { Start(string); Reply(context.Context, string) (string, bool, error); Finalize(context.Context) (builder.Draft, error); Redraft(context.Context, string) (builder.Draft, error); Accept(context.Context, builder.Draft) (string, error) }`
  - `func createLoop(ctx context.Context, c creator, in io.Reader, out, errw io.Writer) int`
  - `func parseAction(line string) (action, arg string)`; `func renderDraft(d builder.Draft) string`

- [ ] **Step 1: Write the failing test**

Replace the body of `internal/cli/cli_test.go` with (note the new `stdin` arg on every `Run` call):

```go
package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/cole/fetch/internal/builder"
	"github.com/cole/fetch/internal/core"
)

func TestParseInputs(t *testing.T) {
	m, err := parseInputs([]string{"part=A1", "year=2024", "note=has=equals"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if m["part"] != "A1" || m["year"] != "2024" || m["note"] != "has=equals" {
		t.Fatalf("parsed = %#v", m)
	}
	if _, err := parseInputs([]string{"bad"}); err == nil {
		t.Fatal("expected error for missing '='")
	}
}

func TestRunUsageWhenNoArgs(t *testing.T) {
	var out, errBuf bytes.Buffer
	if code := Run(nil, strings.NewReader(""), &out, &errBuf); code == 0 {
		t.Fatal("expected non-zero exit for no args")
	}
	if errBuf.Len() == 0 {
		t.Fatal("expected usage on stderr")
	}
}

func TestRunUnknownCommand(t *testing.T) {
	var out, errBuf bytes.Buffer
	if code := Run([]string{"frobnicate"}, strings.NewReader(""), &out, &errBuf); code == 0 {
		t.Fatal("expected non-zero exit for unknown command")
	}
}

func TestParseAction(t *testing.T) {
	a, arg := parseAction("comment rename to Foo")
	if a != "comment" || arg != "rename to Foo" {
		t.Fatalf("got %q %q", a, arg)
	}
	if a, arg := parseAction("accept"); a != "accept" || arg != "" {
		t.Fatalf("got %q %q", a, arg)
	}
}

func TestRenderDraftShowsSchemaAndPlan(t *testing.T) {
	d := builder.Draft{Pipeline: core.Pipeline{
		Name:   "P",
		Schema: []core.Field{{Name: "title", Type: core.FieldString}},
		Plan:   []core.Step{{ID: "search", Type: core.StepSearch}},
	}}
	s := renderDraft(d)
	if !strings.Contains(s, "title") || !strings.Contains(s, "search") {
		t.Fatalf("render missing fields:\n%s", s)
	}
}

// fakeCreator scripts a one-question interview then an accept.
type fakeCreator struct {
	replied  bool
	accepted bool
}

func (f *fakeCreator) Start(string) {}
func (f *fakeCreator) Reply(context.Context, string) (string, bool, error) {
	return "", true, nil // immediately ready
}
func (f *fakeCreator) Finalize(context.Context) (builder.Draft, error) {
	return builder.Draft{Pipeline: core.Pipeline{Name: "P", Schema: []core.Field{{Name: "title"}}}}, nil
}
func (f *fakeCreator) Redraft(context.Context, string) (builder.Draft, error) {
	return builder.Draft{}, nil
}
func (f *fakeCreator) Accept(context.Context, builder.Draft) (string, error) {
	f.accepted = true
	return "p", nil
}

func TestCreateLoopAcceptFlow(t *testing.T) {
	var out, errBuf bytes.Buffer
	in := strings.NewReader("build a thing\naccept\n")
	fc := &fakeCreator{}
	code := createLoop(context.Background(), fc, in, &out, &errBuf)
	if code != 0 {
		t.Fatalf("exit = %d; stderr=%s", code, errBuf.String())
	}
	if !fc.accepted {
		t.Fatal("expected Accept to be called")
	}
	if !strings.Contains(out.String(), "saved pipeline") {
		t.Fatalf("missing save confirmation:\n%s", out.String())
	}
}

func TestCreateLoopCancel(t *testing.T) {
	var out, errBuf bytes.Buffer
	in := strings.NewReader("/cancel\n")
	if code := createLoop(context.Background(), &fakeCreator{}, in, &out, &errBuf); code == 0 {
		t.Fatal("expected non-zero exit on cancel")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=1 go test ./internal/cli/`
Expected: FAIL — `Run` arity mismatch, `undefined: parseAction/renderDraft/createLoop`.

- [ ] **Step 3: Write `internal/cli/create.go`**

```go
package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/cole/fetch/internal/agent"
	"github.com/cole/fetch/internal/builder"
	"github.com/cole/fetch/internal/config"
	"github.com/cole/fetch/internal/pipeline"
	"github.com/cole/fetch/internal/providers/store"
)

// creator is the slice of CreateSession the terminal loop drives (so the loop
// is testable with a fake).
type creator interface {
	Start(goal string)
	Reply(ctx context.Context, msg string) (string, bool, error)
	Finalize(ctx context.Context) (builder.Draft, error)
	Redraft(ctx context.Context, comment string) (builder.Draft, error)
	Accept(ctx context.Context, d builder.Draft) (string, error)
}

func createPipeline(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	cfg, err := config.Load(defaultConfigPath())
	if err != nil {
		fmt.Fprintf(stderr, "warning: config: %v (using defaults)\n", err)
	}
	rowStore, err := store.OpenDuckDB(defaultDBPath(cfg))
	if err != nil {
		fmt.Fprintf(stderr, "error: open store: %v\n", err)
		return 1
	}
	defer rowStore.Close()

	sess := builder.NewSession(builder.SessionDeps{
		LLM:   agent.NewOllama(cfg.Ollama.BaseURL, http.DefaultClient),
		Cfg:   cfg,
		Store: rowStore,
		Repo:  pipeline.NewRepository(cfg.DataDir),
	})
	return createLoop(context.Background(), sess, stdin, stdout, stderr)
}

// createLoop runs the interactive interview -> draft -> accept/comment/cancel loop.
func createLoop(ctx context.Context, c creator, in io.Reader, out, errw io.Writer) int {
	sc := bufio.NewScanner(in)
	fmt.Fprintln(out, "Describe the pipeline you want to build (/cancel to abort):")
	if !sc.Scan() {
		return 1
	}
	goal := strings.TrimSpace(sc.Text())
	if goal == "" || goal == "/cancel" {
		fmt.Fprintln(out, "cancelled")
		return 1
	}
	c.Start(goal)

	q, ready, err := c.Reply(ctx, "")
	for err == nil && !ready {
		if q != "" {
			fmt.Fprintf(out, "\n%s\n> ", q)
		}
		if !sc.Scan() {
			break
		}
		line := strings.TrimSpace(sc.Text())
		if line == "/cancel" {
			fmt.Fprintln(out, "cancelled")
			return 1
		}
		if line == "/done" {
			break
		}
		q, ready, err = c.Reply(ctx, line)
	}
	if err != nil {
		fmt.Fprintf(errw, "interview error: %v\n", err)
		return 1
	}

	draft, err := c.Finalize(ctx)
	if err != nil {
		fmt.Fprintf(errw, "draft error: %v\n", err)
		return 1
	}
	for {
		fmt.Fprint(out, renderDraft(draft))
		fmt.Fprint(out, "\n[accept] save · [comment <text>] revise · [cancel] abort\n> ")
		if !sc.Scan() {
			return 1
		}
		action, arg := parseAction(strings.TrimSpace(sc.Text()))
		switch action {
		case "accept":
			id, err := c.Accept(ctx, draft)
			if err != nil {
				fmt.Fprintf(errw, "save error: %v\n", err)
				return 1
			}
			fmt.Fprintf(out, "saved pipeline %q\n", id)
			return 0
		case "comment":
			draft, err = c.Redraft(ctx, arg)
			if err != nil {
				fmt.Fprintf(errw, "redraft error: %v\n", err)
				return 1
			}
		case "cancel":
			fmt.Fprintln(out, "cancelled")
			return 1
		default:
			fmt.Fprintln(out, "unknown action; type accept, comment <text>, or cancel")
		}
	}
}

func parseAction(line string) (string, string) {
	parts := strings.SplitN(line, " ", 2)
	if len(parts) == 2 {
		return parts[0], strings.TrimSpace(parts[1])
	}
	return parts[0], ""
}

func renderDraft(d builder.Draft) string {
	p := d.Pipeline
	var b strings.Builder
	fmt.Fprintf(&b, "\n=== Draft: %s ===\n", p.Name)
	if p.Description != "" {
		fmt.Fprintf(&b, "%s\n", p.Description)
	}
	b.WriteString("Inputs:\n")
	for _, in := range p.Inputs {
		fmt.Fprintf(&b, "  - %s (%s)\n", in.Name, in.Type)
	}
	b.WriteString("Schema:\n")
	for _, f := range p.Schema {
		fmt.Fprintf(&b, "  - %s (%s)\n", f.Name, f.Type)
	}
	b.WriteString("Plan:\n")
	for _, s := range p.Plan {
		fmt.Fprintf(&b, "  - %s [%s] deps=%v\n", s.ID, s.Type, s.DependsOn)
	}
	return b.String()
}
```

- [ ] **Step 4: Modify `internal/cli/cli.go`**

Change the `Run` signature to accept stdin and add the `create` case. Replace the `Run` function (lines ~32-45) with:

```go
// Run dispatches a CLI invocation and returns a process exit code.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "run":
		return runPipeline(args[1:], stdout, stderr)
	case "create":
		return createPipeline(args[1:], stdin, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}
```

Update the `usage` constant (lines ~26-30) to document `create`:

```go
const usage = `fetch — agentic web research pipelines

Usage:
  fetch create
  fetch run <pipeline.json> [--input key=value ...]
`
```

Wire the real Replanner into the engine in `runPipeline`. Add the import `"github.com/cole/fetch/internal/replanner"` to the import block, and change the `engine.New(engine.Deps{...})` literal (lines ~87-94) to include the Replanner:

```go
	e := engine.New(engine.Deps{
		Config:    cfg,
		LLM:       agent.NewOllama(cfg.Ollama.BaseURL, http.DefaultClient),
		Search:    search.NewTavily(cfg.Search.BaseURL, cfg.APIKey(), http.DefaultClient),
		Fetcher:   fetch.NewHTTP(cfg.Fetch.UserAgent, cfg.Fetch.TimeoutSeconds, cfg.Fetch.MaxBytes),
		Artifacts: artifacts.NewDisk(defaultArtifactDir(cfg)),
		Store:     rowStore,
		Replanner: replanner.New(agent.NewOllama(cfg.Ollama.BaseURL, http.DefaultClient), cfg),
	})
```

- [ ] **Step 5: Modify `cmd/fetch/main.go`**

```go
package main

import (
	"os"

	"github.com/cole/fetch/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
```

- [ ] **Step 6: Run tests + build to verify**

Run: `CGO_ENABLED=1 go test ./... && CGO_ENABLED=1 go build ./... && CGO_ENABLED=1 go vet ./... && gofmt -l internal/ cmd/`
Expected: all packages PASS, binary builds, vet clean, gofmt empty. (The `create` network path runs only when the binary is invoked against real Ollama.)

- [ ] **Step 7: Manual smoke (optional, requires real services)**

```bash
go run ./cmd/fetch create
```
Answer the interview prompts, accept, and confirm a `pipelines/<id>.json` appears under the data dir. Skip if Ollama models aren't pulled.

- [ ] **Step 8: Commit**

```bash
git add internal/cli/create.go internal/cli/cli.go internal/cli/cli_test.go cmd/fetch/main.go
git commit -m "feat: fetch create CLI and replanner wiring for fetch run"
```

---

## Agents Definition of Done

- `go build ./...`, `go test ./...`, `go vet ./...` pass with `CGO_ENABLED=1`; `gofmt -l internal/ cmd/` empty.
- A scripted interview (FakeLLM) converges, produces a draft that passes `pipeline.Validate`, and accept writes a `pipelines/<id>.json` + ensures a results table — all in `internal/builder` tests.
- The validate-repair loop is proven: an initially-invalid plan is corrected and saved; exhaustion surfaces an error.
- `replanner.New(...)` satisfies `engine.Replanner`; an engine run with a failing step recovers via LLM `adapt` and yields a self-heal candidate (integration test).
- `fetch create` runs interview → draft → accept/comment/cancel over stdin/stdout; `fetch run` injects the real Replanner.

## Self-Review Notes

- **Spec coverage:** Interviewer ✓ (T1), SchemaDesigner ✓ (T2), Planner + bounded validate-repair ✓ (T3), CreateSession with redraft + accept(ensure-table+save) ✓ (T4), LLM Replanner satisfying the engine seam ✓ (T5), `fetch create` CLI + `run` Replanner wiring ✓ (T6). Router cut per spec (config role string untouched). Golden-file prompt testing is implemented as the prompt assertions in T1 (`TestInterviewSystemPromptOneAtATime`) and the schema/field-name presence implied by structured-output schemas; full checked-in golden files were judged unnecessary given the prompts are fully specified and small — the reviewer should confirm this substitution is acceptable or request testdata goldens.
- **Type consistency:** `Facts`/`Draft`/`designOutput` wrap `core` types with matching JSON tags (`output_fields`, `inputs`, `schema`, `plan`, `depends_on`); `Decision`/`ReplanRequest` come from `engine` unchanged. `Run`'s new `stdin io.Reader` arg is threaded through `main.go` and every test call site.
- **No engine changes:** Plan 3 only fills `Deps.Replanner` and consumes `pipeline`/`core`/`store` as-is. Import directions: `builder → {core,agent,config,pipeline,store}`; `replanner → {core,agent,config,engine}`; `cli → {builder,replanner,engine,...}`. No cycles (`engine` imports none of these).
- **Determinism:** `CreateSession` takes `Now`/`IDGen`; the Replanner is stateless. `slugify` + `IDGen`-on-collision keeps IDs stable in tests.
