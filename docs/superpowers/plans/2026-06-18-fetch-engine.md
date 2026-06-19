# Fetch Engine Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the execution engine — a pipeline repository + validator, parameter templating, the five step executors, dependency-ordered run orchestration with progress events, and the bounded agent-fallback + self-heal mechanism — so a hand-written pipeline JSON can be run end to end (`search → fetch → extract → store`) against the foundation providers.

**Architecture:** `internal/pipeline` owns the durable pipeline definition (JSON load/save, validation, topological ordering, `{{...}}` parameter resolution). `internal/engine` walks a validated plan in dependency order, dispatches each step to a deterministic executor (or the LLM for `extract`), records `Run`/`StepTrace` to the store, streams `Event`s on a channel, and on step failure consults a pluggable `Replanner` (the fallback *mechanism* lives here; the LLM-backed Replanner itself is Plan 3). Successful run-time adaptations are captured as self-heal candidate revisions and optionally auto-promoted.

**Tech Stack:** Go 1.26, stdlib only (`encoding/json`, `regexp`, `context`, `sync`), building on the Plan 1 packages (`internal/core`, `internal/config`, `internal/agent`, `internal/providers/*`).

This is **Plan 2 of 4** (Foundation ✓ → Engine → Agents → TUI). Its deliverable is a tested engine plus a thin `fetch run` CLI; the conversational pipeline-builder (Interviewer/Planner) and the LLM-backed Replanner come in Plan 3, and the TUI in Plan 4.

## Global Constraints

- Module path `github.com/cole/fetch`; Go floor `go 1.26`; `CGO_ENABLED=1`.
- Build on the existing foundation interfaces verbatim — do NOT change their signatures:
  - `agent.LLM.Chat(ctx, agent.ChatRequest) (agent.ChatResponse, error)` (`ChatRequest` has `Model`, `Messages`, `Format json.RawMessage`, `Temperature`).
  - `search.Search.Search(ctx, query string, search.Options) ([]search.Result, error)`; `search.Result{Title,URL,Snippet,Content string; Score float64}`.
  - `fetch.Fetcher.Fetch(ctx, url string, fetch.Method) (fetch.Page, error)`; `fetch.Page{URL string; StatusCode int; ContentType string; Raw []byte; Text string}`; `fetch.MethodHTTP`.
  - `artifacts.Store.Put(ctx, runID, stepID string, data []byte, ext string) (ref string, err error)` and `Get(ctx, ref) ([]byte, error)`.
  - `store.Store.{EnsureTable(ctx, pipelineID, []core.Field), AppendRows(ctx, pipelineID, []core.Field, runID, []map[string]any), Query(ctx, sql), RecordRun(ctx, core.Run), RecordTrace(ctx, core.StepTrace), Close()}`.
  - `config.Config.ModelFor(role)`, role const `config.RoleExtract`.
- Domain types come from `internal/core` only — never redefine `Pipeline`/`Step`/`Field`/`Run`/`StepTrace`.
- Default retry budget is **2 per step** (`MaxRetries: 2`), configurable via `engine.Deps`.
- All new external-ish seams (`Replanner`) are interfaces; the engine takes `nil` replanner to mean "no fallback".
- Determinism for tests: the engine takes injectable `Now func() time.Time` and `IDGen func() string`; tests inject fixed ones. Do NOT call `time.Now()` directly inside `Run`.
- Step executors fetch over the network only via the injected providers; engine unit tests use the foundation fakes (`agent.FakeLLM`, `search.FakeSearch`, `fetch.FakeFetcher`) and the new `store.FakeStore`/`artifacts.FakeArtifacts` — NO real network in `go test ./...`.
- `gofmt`/`go vet ./...` clean before each commit.

### Step output & param conventions (shared contract — every executor task depends on this)

Each executor returns a `stepResult` whose `output map[string]any` is stored in the run scope under the step's ID and is referenceable by later steps via `{{steps.<id>.<field>}}`. Inputs are referenceable via `{{input.<name>}}`. The conventional output fields:

| Step | Reads (params) | Produces (output fields) |
|------|----------------|--------------------------|
| `search` | `query string`, optional `max_results int` | `results []search.Result`, `urls []string`, `count int` |
| `fetch` | `urls []string` (e.g. `"{{steps.search.urls}}"`), optional `method string` | `pages []fetch.Page`, `count int` |
| `extract` | `pages []fetch.Page` or `results []search.Result` or `text string` | `rows []map[string]any` |
| `transform` | `rows []map[string]any`, `op string` (`dedup`/`limit`), optional `by []string`/`n int` | `rows []map[string]any` |
| `store` | `rows []map[string]any` | `stored int` |

A param string that is **exactly** one `{{ref}}` resolves to the referenced value with its concrete type preserved (so `urls` stays `[]string`). A param string with surrounding text interpolates refs as `fmt.Sprintf("%v", …)`.

---

### Task 1: Pipeline repository and validation

**Files:**
- Create: `internal/pipeline/repo.go`
- Create: `internal/pipeline/validate.go`
- Create: `internal/pipeline/repo_test.go`
- Create: `internal/pipeline/validate_test.go`

**Interfaces:**
- Consumes: `core.Pipeline`, `core.Step`, `core.StepType` constants (Plan 1).
- Produces:
  - `type Repository struct{ ... }`; `func NewRepository(dataDir string) *Repository` (operates on `{dataDir}/pipelines`); `Save(core.Pipeline) error` (writes `{id}.json`, indented, creating the dir); `Load(id string) (core.Pipeline, error)`; `List() ([]core.Pipeline, error)`; `Delete(id string) error`.
  - `func Validate(p core.Pipeline) error`; `func TopoOrder(p core.Pipeline) ([]core.Step, error)` (Kahn's algorithm, preserving plan order among ready nodes; errors on duplicate IDs, unknown deps, or cycles).

- [ ] **Step 1: Write the failing tests**

Create `internal/pipeline/validate_test.go`:

```go
package pipeline

import (
	"testing"

	"github.com/cole/fetch/internal/core"
)

func pipe(steps ...core.Step) core.Pipeline {
	return core.Pipeline{ID: "p1", Plan: steps}
}

func TestTopoOrderLinear(t *testing.T) {
	p := pipe(
		core.Step{ID: "c", Type: core.StepStore, DependsOn: []string{"b"}},
		core.Step{ID: "a", Type: core.StepSearch},
		core.Step{ID: "b", Type: core.StepFetch, DependsOn: []string{"a"}},
	)
	order, err := TopoOrder(p)
	if err != nil {
		t.Fatalf("topo: %v", err)
	}
	got := []string{order[0].ID, order[1].ID, order[2].ID}
	want := []string{"a", "b", "c"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

func TestTopoOrderCycle(t *testing.T) {
	p := pipe(
		core.Step{ID: "a", Type: core.StepSearch, DependsOn: []string{"b"}},
		core.Step{ID: "b", Type: core.StepFetch, DependsOn: []string{"a"}},
	)
	if _, err := TopoOrder(p); err == nil {
		t.Fatal("expected cycle error")
	}
}

func TestTopoOrderUnknownDep(t *testing.T) {
	p := pipe(core.Step{ID: "a", Type: core.StepSearch, DependsOn: []string{"ghost"}})
	if _, err := TopoOrder(p); err == nil {
		t.Fatal("expected unknown-dep error")
	}
}

func TestValidate(t *testing.T) {
	good := pipe(core.Step{ID: "a", Type: core.StepSearch})
	if err := Validate(good); err != nil {
		t.Fatalf("good pipeline rejected: %v", err)
	}
	if err := Validate(core.Pipeline{ID: "x"}); err == nil {
		t.Fatal("empty plan should be invalid")
	}
	if err := Validate(core.Pipeline{ID: "", Plan: []core.Step{{ID: "a", Type: core.StepSearch}}}); err == nil {
		t.Fatal("empty id should be invalid")
	}
	bad := pipe(core.Step{ID: "a", Type: core.StepType("nonsense")})
	if err := Validate(bad); err == nil {
		t.Fatal("invalid step type should be rejected")
	}
	dup := pipe(core.Step{ID: "a", Type: core.StepSearch}, core.Step{ID: "a", Type: core.StepFetch})
	if err := Validate(dup); err == nil {
		t.Fatal("duplicate step id should be rejected")
	}
}
```

Create `internal/pipeline/repo_test.go`:

```go
package pipeline

import (
	"testing"

	"github.com/cole/fetch/internal/core"
)

func TestRepositorySaveLoadListDelete(t *testing.T) {
	r := NewRepository(t.TempDir())
	p := core.Pipeline{ID: "truck-xref", Name: "Truck cross-ref", Version: 1,
		Plan: []core.Step{{ID: "s", Type: core.StepSearch}}}
	if err := r.Save(p); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := r.Load("truck-xref")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Name != "Truck cross-ref" || got.Version != 1 {
		t.Fatalf("loaded wrong: %+v", got)
	}
	list, err := r.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].ID != "truck-xref" {
		t.Fatalf("list wrong: %+v", list)
	}
	if err := r.Delete("truck-xref"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := r.Load("truck-xref"); err == nil {
		t.Fatal("expected load error after delete")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/pipeline/`
Expected: FAIL — `undefined: NewRepository`, `undefined: Validate`, `undefined: TopoOrder`.

- [ ] **Step 3: Write `internal/pipeline/validate.go`**

```go
// Package pipeline owns the durable pipeline definition: persistence,
// validation, dependency ordering, and parameter templating.
package pipeline

import (
	"errors"
	"fmt"

	"github.com/cole/fetch/internal/core"
)

// Validate checks a pipeline is well-formed enough to execute.
func Validate(p core.Pipeline) error {
	if p.ID == "" {
		return errors.New("pipeline id is empty")
	}
	if len(p.Plan) == 0 {
		return errors.New("pipeline has no steps")
	}
	for _, s := range p.Plan {
		switch s.Type {
		case core.StepSearch, core.StepFetch, core.StepExtract, core.StepTransform, core.StepStore:
		default:
			return fmt.Errorf("step %q has invalid type %q", s.ID, s.Type)
		}
	}
	if _, err := TopoOrder(p); err != nil {
		return err
	}
	return nil
}

// TopoOrder returns the steps in dependency order using Kahn's algorithm,
// preserving plan order among ready nodes. It errors on duplicate step IDs,
// dependencies on unknown steps, or cycles.
func TopoOrder(p core.Pipeline) ([]core.Step, error) {
	byID := make(map[string]core.Step, len(p.Plan))
	indeg := make(map[string]int, len(p.Plan))
	for _, s := range p.Plan {
		if _, dup := byID[s.ID]; dup {
			return nil, fmt.Errorf("duplicate step id %q", s.ID)
		}
		byID[s.ID] = s
		indeg[s.ID] = 0
	}
	adj := make(map[string][]string)
	for _, s := range p.Plan {
		for _, dep := range s.DependsOn {
			if _, ok := byID[dep]; !ok {
				return nil, fmt.Errorf("step %q depends on unknown step %q", s.ID, dep)
			}
			adj[dep] = append(adj[dep], s.ID)
			indeg[s.ID]++
		}
	}
	var queue []string
	for _, s := range p.Plan { // plan order among ready nodes
		if indeg[s.ID] == 0 {
			queue = append(queue, s.ID)
		}
	}
	order := make([]core.Step, 0, len(p.Plan))
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		order = append(order, byID[id])
		for _, nb := range adj[id] {
			indeg[nb]--
			if indeg[nb] == 0 {
				queue = append(queue, nb)
			}
		}
	}
	if len(order) != len(p.Plan) {
		return nil, errors.New("plan has a dependency cycle")
	}
	return order, nil
}
```

- [ ] **Step 4: Write `internal/pipeline/repo.go`**

```go
package pipeline

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cole/fetch/internal/core"
)

// Repository persists pipeline definitions as JSON files under a directory.
type Repository struct {
	dir string
}

// NewRepository roots the repository at {dataDir}/pipelines.
func NewRepository(dataDir string) *Repository {
	return &Repository{dir: filepath.Join(dataDir, "pipelines")}
}

func (r *Repository) path(id string) string {
	return filepath.Join(r.dir, id+".json")
}

// Save writes the pipeline as indented JSON, creating the directory.
func (r *Repository) Save(p core.Pipeline) error {
	if p.ID == "" {
		return fmt.Errorf("cannot save pipeline with empty id")
	}
	if err := os.MkdirAll(r.dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(r.path(p.ID), b, 0o644)
}

// Load reads one pipeline by ID.
func (r *Repository) Load(id string) (core.Pipeline, error) {
	b, err := os.ReadFile(r.path(id))
	if err != nil {
		return core.Pipeline{}, err
	}
	var p core.Pipeline
	if err := json.Unmarshal(b, &p); err != nil {
		return core.Pipeline{}, fmt.Errorf("decode pipeline %q: %w", id, err)
	}
	return p, nil
}

// List loads every pipeline in the directory (missing dir → empty list).
func (r *Repository) List() ([]core.Pipeline, error) {
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []core.Pipeline
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		p, err := r.Load(id)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// Delete removes one pipeline definition.
func (r *Repository) Delete(id string) error {
	return os.Remove(r.path(id))
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/pipeline/ && go vet ./internal/pipeline/`
Expected: PASS, vet clean.

- [ ] **Step 6: Commit** (skip if the run is configured for end-of-run commits)

```bash
git add internal/pipeline/repo.go internal/pipeline/validate.go internal/pipeline/repo_test.go internal/pipeline/validate_test.go
git commit -m "feat: pipeline repository and plan validation"
```

---

### Task 2: Parameter templating

**Files:**
- Create: `internal/pipeline/template.go`
- Create: `internal/pipeline/template_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces:
  - `type Scope struct { Input map[string]any; Steps map[string]map[string]any }`
  - `func Resolve(params map[string]any, sc Scope) (map[string]any, error)` — resolves `{{input.x}}` and `{{steps.id.field}}` references. A value that is exactly one reference keeps the referenced value's concrete type; a value with surrounding text interpolates with `fmt.Sprintf("%v", …)`. Recurses into nested `[]any` and `map[string]any`. Unknown references are an error.

- [ ] **Step 1: Write the failing test**

Create `internal/pipeline/template_test.go`:

```go
package pipeline

import (
	"reflect"
	"testing"
)

func TestResolveWholeValuePreservesType(t *testing.T) {
	sc := Scope{
		Input: map[string]any{"part": "12345"},
		Steps: map[string]map[string]any{
			"search": {"urls": []string{"https://a", "https://b"}},
		},
	}
	out, err := Resolve(map[string]any{
		"query": "{{input.part}} cross reference",
		"urls":  "{{steps.search.urls}}",
	}, sc)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if out["query"] != "12345 cross reference" {
		t.Fatalf("query = %v", out["query"])
	}
	urls, ok := out["urls"].([]string)
	if !ok || !reflect.DeepEqual(urls, []string{"https://a", "https://b"}) {
		t.Fatalf("urls not preserved as []string: %#v", out["urls"])
	}
}

func TestResolveNested(t *testing.T) {
	sc := Scope{Input: map[string]any{"n": 5}}
	out, err := Resolve(map[string]any{
		"opts": map[string]any{"max": "{{input.n}}"},
		"list": []any{"{{input.n}}", "static"},
	}, sc)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	opts := out["opts"].(map[string]any)
	if opts["max"] != 5 { // whole-value, type preserved (int)
		t.Fatalf("nested whole-value = %#v", opts["max"])
	}
	list := out["list"].([]any)
	if list[0] != 5 || list[1] != "static" {
		t.Fatalf("list = %#v", list)
	}
}

func TestResolveUnknownReference(t *testing.T) {
	if _, err := Resolve(map[string]any{"x": "{{steps.nope.field}}"}, Scope{}); err == nil {
		t.Fatal("expected error for unknown reference")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/pipeline/ -run TestResolve`
Expected: FAIL — `undefined: Resolve`, `undefined: Scope`.

- [ ] **Step 3: Write `internal/pipeline/template.go`**

```go
package pipeline

import (
	"fmt"
	"regexp"
	"strings"
)

// Scope holds the values references can resolve against during a run.
type Scope struct {
	Input map[string]any
	Steps map[string]map[string]any
}

var refRe = regexp.MustCompile(`{{\s*([a-zA-Z0-9_.]+)\s*}}`)

// Resolve walks params and replaces {{...}} references using sc.
func Resolve(params map[string]any, sc Scope) (map[string]any, error) {
	out := make(map[string]any, len(params))
	for k, v := range params {
		rv, err := resolveValue(v, sc)
		if err != nil {
			return nil, err
		}
		out[k] = rv
	}
	return out, nil
}

func resolveValue(v any, sc Scope) (any, error) {
	switch t := v.(type) {
	case string:
		return resolveString(t, sc)
	case []any:
		res := make([]any, len(t))
		for i, e := range t {
			rv, err := resolveValue(e, sc)
			if err != nil {
				return nil, err
			}
			res[i] = rv
		}
		return res, nil
	case map[string]any:
		return Resolve(t, sc)
	default:
		return v, nil
	}
}

func resolveString(s string, sc Scope) (any, error) {
	trimmed := strings.TrimSpace(s)
	if m := refRe.FindStringSubmatch(trimmed); m != nil && m[0] == trimmed {
		val, ok := lookup(m[1], sc)
		if !ok {
			return nil, fmt.Errorf("unresolved reference %q", m[1])
		}
		return val, nil
	}
	var firstErr error
	res := refRe.ReplaceAllStringFunc(s, func(tok string) string {
		name := refRe.FindStringSubmatch(tok)[1]
		val, ok := lookup(name, sc)
		if !ok {
			if firstErr == nil {
				firstErr = fmt.Errorf("unresolved reference %q", name)
			}
			return ""
		}
		return fmt.Sprintf("%v", val)
	})
	if firstErr != nil {
		return nil, firstErr
	}
	return res, nil
}

func lookup(name string, sc Scope) (any, bool) {
	parts := strings.Split(name, ".")
	switch parts[0] {
	case "input":
		if len(parts) != 2 {
			return nil, false
		}
		v, ok := sc.Input[parts[1]]
		return v, ok
	case "steps":
		if len(parts) != 3 {
			return nil, false
		}
		fields, ok := sc.Steps[parts[1]]
		if !ok {
			return nil, false
		}
		v, ok := fields[parts[2]]
		return v, ok
	}
	return nil, false
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/pipeline/ && go vet ./internal/pipeline/`
Expected: PASS, vet clean.

- [ ] **Step 5: Commit**

```bash
git add internal/pipeline/template.go internal/pipeline/template_test.go
git commit -m "feat: pipeline parameter templating"
```

---

### Task 3: In-memory fakes for store and artifacts

**Files:**
- Create: `internal/providers/store/fake.go`
- Create: `internal/providers/store/fake_test.go`
- Create: `internal/providers/artifacts/fake.go`
- Create: `internal/providers/artifacts/fake_test.go`

**Interfaces:**
- Consumes: `store.Store`, `artifacts.Store` interfaces (Plan 1), `core.Field/Run/StepTrace`.
- Produces:
  - `store.FakeStore` implementing `store.Store`, with exported fields `Tables map[string][]core.Field`, `Rows map[string][]map[string]any`, `Runs []core.Run`, `Traces []core.StepTrace`; `func NewFakeStore() *FakeStore`. `Query` returns `nil, nil` (not used by the engine). Concurrency-safe via an internal mutex.
  - `artifacts.FakeArtifacts` implementing `artifacts.Store`, storing bytes in memory keyed by a generated ref; `func NewFakeArtifacts() *FakeArtifacts`. Concurrency-safe.

- [ ] **Step 1: Write the failing tests**

Create `internal/providers/store/fake_test.go`:

```go
package store

import (
	"context"
	"testing"

	"github.com/cole/fetch/internal/core"
)

func TestFakeStoreImplementsStore(t *testing.T) {
	var _ Store = NewFakeStore()
}

func TestFakeStoreAppendAndRecord(t *testing.T) {
	ctx := context.Background()
	fs := NewFakeStore()
	fields := []core.Field{{Name: "x", Type: core.FieldString}}
	if err := fs.EnsureTable(ctx, "p1", fields); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if err := fs.AppendRows(ctx, "p1", fields, "run1", []map[string]any{{"x": "a"}, {"x": "b"}}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if got := len(fs.Rows["p1"]); got != 2 {
		t.Fatalf("rows = %d", got)
	}
	if err := fs.RecordRun(ctx, core.Run{ID: "run1", Status: core.RunOK}); err != nil {
		t.Fatalf("record run: %v", err)
	}
	if err := fs.RecordTrace(ctx, core.StepTrace{RunID: "run1", StepID: "s"}); err != nil {
		t.Fatalf("record trace: %v", err)
	}
	if len(fs.Runs) != 1 || len(fs.Traces) != 1 {
		t.Fatalf("runs=%d traces=%d", len(fs.Runs), len(fs.Traces))
	}
}
```

Create `internal/providers/artifacts/fake_test.go`:

```go
package artifacts

import (
	"bytes"
	"context"
	"testing"
)

func TestFakeArtifactsImplementsStore(t *testing.T) {
	var _ Store = NewFakeArtifacts()
}

func TestFakeArtifactsRoundTrip(t *testing.T) {
	ctx := context.Background()
	fa := NewFakeArtifacts()
	ref, err := fa.Put(ctx, "run1", "s1", []byte("hello"), "txt")
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err := fa.Get(ctx, ref)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !bytes.Equal(got, []byte("hello")) {
		t.Fatalf("got %q", got)
	}
	if _, err := fa.Get(ctx, "missing"); err == nil {
		t.Fatal("expected error for missing ref")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/providers/store/ ./internal/providers/artifacts/ -run Fake`
Expected: FAIL — `undefined: NewFakeStore`, `undefined: NewFakeArtifacts`.

- [ ] **Step 3: Write `internal/providers/store/fake.go`**

```go
package store

import (
	"context"
	"sync"

	"github.com/cole/fetch/internal/core"
)

var _ Store = (*FakeStore)(nil)

// FakeStore is an in-memory Store for engine tests (no cgo/DuckDB).
type FakeStore struct {
	mu     sync.Mutex
	Tables map[string][]core.Field
	Rows   map[string][]map[string]any
	Runs   []core.Run
	Traces []core.StepTrace
}

func NewFakeStore() *FakeStore {
	return &FakeStore{
		Tables: map[string][]core.Field{},
		Rows:   map[string][]map[string]any{},
	}
}

func (f *FakeStore) EnsureTable(_ context.Context, pipelineID string, fields []core.Field) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Tables[pipelineID] = fields
	return nil
}

func (f *FakeStore) AppendRows(_ context.Context, pipelineID string, _ []core.Field, runID string, rows []map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range rows {
		cp := make(map[string]any, len(r)+1)
		for k, v := range r {
			cp[k] = v
		}
		cp["__run_id"] = runID
		f.Rows[pipelineID] = append(f.Rows[pipelineID], cp)
	}
	return nil
}

func (f *FakeStore) Query(_ context.Context, _ string) ([]map[string]any, error) {
	return nil, nil
}

func (f *FakeStore) RecordRun(_ context.Context, r core.Run) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Runs = append(f.Runs, r)
	return nil
}

func (f *FakeStore) RecordTrace(_ context.Context, t core.StepTrace) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Traces = append(f.Traces, t)
	return nil
}

func (f *FakeStore) Close() error { return nil }
```

- [ ] **Step 4: Write `internal/providers/artifacts/fake.go`**

```go
package artifacts

import (
	"context"
	"fmt"
	"sync"
)

var _ Store = (*FakeArtifacts)(nil)

// FakeArtifacts is an in-memory artifact Store for engine tests.
type FakeArtifacts struct {
	mu   sync.Mutex
	data map[string][]byte
	seq  int
}

func NewFakeArtifacts() *FakeArtifacts {
	return &FakeArtifacts{data: map[string][]byte{}}
}

func (f *FakeArtifacts) Put(_ context.Context, runID, stepID string, data []byte, ext string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ref := fmt.Sprintf("%s/%s/%d.%s", runID, stepID, f.seq, ext)
	f.seq++
	cp := make([]byte, len(data))
	copy(cp, data)
	f.data[ref] = cp
	return ref, nil
}

func (f *FakeArtifacts) Get(_ context.Context, ref string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.data[ref]
	if !ok {
		return nil, fmt.Errorf("artifact not found: %s", ref)
	}
	return b, nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/providers/store/ ./internal/providers/artifacts/ && go vet ./internal/providers/store/ ./internal/providers/artifacts/`
Expected: PASS, vet clean.

- [ ] **Step 6: Commit**

```bash
git add internal/providers/store/fake.go internal/providers/store/fake_test.go internal/providers/artifacts/fake.go internal/providers/artifacts/fake_test.go
git commit -m "feat: in-memory fakes for store and artifacts"
```

---

### Task 4: Engine core — orchestration, deterministic executors, fallback mechanism

**Files:**
- Create: `internal/engine/event.go`
- Create: `internal/engine/engine.go`
- Create: `internal/engine/executors.go`
- Create: `internal/engine/convert.go`
- Create: `internal/engine/engine_test.go`

**Interfaces:**
- Consumes: `pipeline.{Validate,TopoOrder,Resolve,Scope,Repository}`, all provider interfaces + their fakes, `core.*`, `config.Config`.
- Produces:
  - `type EventType string` + constants `EventRunStarted/EventStepStarted/EventStepRetry/EventFallback/EventStepFinished/EventRunFinished`; `type Event struct { RunID, StepID string; Type EventType; Message, Status string }`.
  - `type Decision struct { Action string; Step core.Step; Reason string }` + constants `ActionAdapt="adapt"`, `ActionSkip="skip"`, `ActionAbort="abort"`; `type ReplanRequest struct { Pipeline core.Pipeline; Step core.Step; Params map[string]any; Attempt int; Err string }`; `type Replanner interface { Replan(ctx, ReplanRequest) (Decision, error) }`.
  - `type Revision struct { StepID string; Original, Adapted core.Step; RunID string }`; `type RunResult struct { Run core.Run; Traces []core.StepTrace; Candidates []Revision }`.
  - `type Deps struct { Config config.Config; LLM agent.LLM; Search search.Search; Fetcher fetch.Fetcher; Artifacts artifacts.Store; Store store.Store; Replanner Replanner; Repo *pipeline.Repository; MaxRetries int; AutoPromote bool; Now func() time.Time; IDGen func() string }`; `func New(d Deps) *Engine`.
  - `func (e *Engine) Run(ctx context.Context, p core.Pipeline, input map[string]any, events chan<- Event) (RunResult, error)`.
  - Deterministic executors for `search`, `fetch`, `transform`, `store`. `extract` returns a placeholder error here (wired in Task 5).

- [ ] **Step 1: Write the failing test**

Create `internal/engine/engine_test.go`:

```go
package engine

import (
	"context"
	"testing"
	"time"

	"github.com/cole/fetch/internal/config"
	"github.com/cole/fetch/internal/core"
	"github.com/cole/fetch/internal/providers/artifacts"
	"github.com/cole/fetch/internal/providers/fetch"
	"github.com/cole/fetch/internal/providers/search"
	"github.com/cole/fetch/internal/providers/store"
)

// queryAwareSearch fails until the query becomes "good" — used to drive the
// fallback/self-heal path.
type queryAwareSearch struct{ calls []string }

func (q *queryAwareSearch) Search(_ context.Context, query string, _ search.Options) ([]search.Result, error) {
	q.calls = append(q.calls, query)
	if query != "good" {
		return nil, nil // empty → engine treats as failure
	}
	return []search.Result{{Title: "T", URL: "https://x", Content: "body"}}, nil
}

type fakeReplanner struct {
	decisions []Decision
	calls     int
}

func (f *fakeReplanner) Replan(_ context.Context, _ ReplanRequest) (Decision, error) {
	d := f.decisions[min(f.calls, len(f.decisions)-1)]
	f.calls++
	return d, nil
}

func fixedDeps(d Deps) Deps {
	if d.Config.Search.MaxResults == 0 {
		d.Config = config.Default()
	}
	d.Now = func() time.Time { return time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC) }
	n := 0
	d.IDGen = func() string { n++; return "run-fixed" }
	return d
}

func storePipeline() core.Pipeline {
	return core.Pipeline{
		ID:     "p1",
		Schema: []core.Field{{Name: "x", Type: core.FieldString}},
		Plan: []core.Step{
			{ID: "store", Type: core.StepStore, Params: map[string]any{"rows": "{{input.rows}}"}},
		},
	}
}

func TestRunStoresRowsAndRecords(t *testing.T) {
	fs := store.NewFakeStore()
	e := New(fixedDeps(Deps{
		Store:     fs,
		Artifacts: artifacts.NewFakeArtifacts(),
	}))
	res, err := e.Run(context.Background(), storePipeline(),
		map[string]any{"rows": []any{map[string]any{"x": "a"}}}, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Run.Status != core.RunOK {
		t.Fatalf("status = %s", res.Run.Status)
	}
	if len(fs.Rows["p1"]) != 1 {
		t.Fatalf("rows = %d", len(fs.Rows["p1"]))
	}
	if len(fs.Traces) != 1 || fs.Traces[0].Status != "ok" {
		t.Fatalf("traces = %+v", fs.Traces)
	}
	// RecordRun called at start (running) and end (ok).
	if len(fs.Runs) != 2 || fs.Runs[1].Status != core.RunOK {
		t.Fatalf("runs = %+v", fs.Runs)
	}
}

func TestRunEmitsEventsInOrder(t *testing.T) {
	events := make(chan Event, 16)
	e := New(fixedDeps(Deps{Store: store.NewFakeStore(), Artifacts: artifacts.NewFakeArtifacts()}))
	_, err := e.Run(context.Background(), storePipeline(),
		map[string]any{"rows": []any{}}, events)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	close(events)
	var types []EventType
	for ev := range events {
		types = append(types, ev.Type)
	}
	want := []EventType{EventRunStarted, EventStepStarted, EventStepFinished, EventRunFinished}
	if len(types) != len(want) {
		t.Fatalf("events = %v", types)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Fatalf("events = %v, want %v", types, want)
		}
	}
}

func TestSearchFetchHappyPath(t *testing.T) {
	srch := &queryAwareSearch{}
	ff := &fetch.FakeFetcher{Pages: map[string]fetch.Page{
		"https://x": {URL: "https://x", StatusCode: 200, ContentType: "text/html", Raw: []byte("<html>body</html>"), Text: "body"},
	}}
	fs := store.NewFakeStore()
	e := New(fixedDeps(Deps{Search: srch, Fetcher: ff, Store: fs, Artifacts: artifacts.NewFakeArtifacts()}))
	p := core.Pipeline{ID: "p2", Plan: []core.Step{
		{ID: "search", Type: core.StepSearch, Params: map[string]any{"query": "good"}},
		{ID: "fetch", Type: core.StepFetch, DependsOn: []string{"search"}, Params: map[string]any{"urls": "{{steps.search.urls}}"}},
	}}
	res, err := e.Run(context.Background(), p, nil, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Run.Status != core.RunOK {
		t.Fatalf("status = %s; traces=%+v", res.Run.Status, res.Traces)
	}
	if len(ff.URLs) != 1 || ff.URLs[0] != "https://x" {
		t.Fatalf("fetcher urls = %v", ff.URLs)
	}
}

func TestFallbackAdaptSelfHeal(t *testing.T) {
	srch := &queryAwareSearch{}
	repl := &fakeReplanner{decisions: []Decision{{
		Action: ActionAdapt,
		Step:   core.Step{ID: "search", Type: core.StepSearch, Params: map[string]any{"query": "good"}},
		Reason: "broaden",
	}}}
	repo := newTempRepo(t)
	e := New(fixedDeps(Deps{
		Search: srch, Store: store.NewFakeStore(), Artifacts: artifacts.NewFakeArtifacts(),
		Replanner: repl, Repo: repo, AutoPromote: true, MaxRetries: 2,
	}))
	p := core.Pipeline{ID: "p3", Version: 1, Plan: []core.Step{
		{ID: "search", Type: core.StepSearch, Params: map[string]any{"query": "bad"}},
	}}
	if err := repo.Save(p); err != nil {
		t.Fatalf("seed save: %v", err)
	}
	res, err := e.Run(context.Background(), p, nil, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Run.Status != core.RunOK {
		t.Fatalf("status = %s", res.Run.Status)
	}
	if len(res.Candidates) != 1 || res.Candidates[0].Adapted.Params["query"] != "good" {
		t.Fatalf("candidates = %+v", res.Candidates)
	}
	if !res.Traces[0].FallbackUsed {
		t.Fatal("expected FallbackUsed on trace")
	}
	// auto-promote saved a bumped pipeline with the adapted step.
	saved, err := repo.Load("p3")
	if err != nil {
		t.Fatalf("load promoted: %v", err)
	}
	if saved.Version != 2 || saved.Plan[0].Params["query"] != "good" {
		t.Fatalf("not promoted: %+v", saved)
	}
}

func TestFallbackRetryBudgetExhausted(t *testing.T) {
	srch := &queryAwareSearch{}
	repl := &fakeReplanner{decisions: []Decision{{
		Action: ActionAdapt,
		Step:   core.Step{ID: "search", Type: core.StepSearch, Params: map[string]any{"query": "still-bad"}},
	}}}
	e := New(fixedDeps(Deps{Search: srch, Store: store.NewFakeStore(), Artifacts: artifacts.NewFakeArtifacts(), Replanner: repl, MaxRetries: 2}))
	p := core.Pipeline{ID: "p4", Plan: []core.Step{
		{ID: "search", Type: core.StepSearch, Params: map[string]any{"query": "bad"}},
	}}
	res, err := e.Run(context.Background(), p, nil, nil)
	if err == nil {
		t.Fatal("expected run error after exhausting retries")
	}
	if res.Run.Status != core.RunFailed {
		t.Fatalf("status = %s", res.Run.Status)
	}
	// 1 initial + 2 retries = 3 search attempts.
	if len(srch.calls) != 3 {
		t.Fatalf("search attempts = %d", len(srch.calls))
	}
}

func TestFallbackSkip(t *testing.T) {
	srch := &queryAwareSearch{}
	repl := &fakeReplanner{decisions: []Decision{{Action: ActionSkip}}}
	e := New(fixedDeps(Deps{Search: srch, Store: store.NewFakeStore(), Artifacts: artifacts.NewFakeArtifacts(), Replanner: repl, MaxRetries: 2}))
	p := core.Pipeline{ID: "p5", Plan: []core.Step{
		{ID: "search", Type: core.StepSearch, Params: map[string]any{"query": "bad"}},
	}}
	res, err := e.Run(context.Background(), p, nil, nil)
	if err != nil {
		t.Fatalf("skip should not error: %v", err)
	}
	if res.Run.Status != core.RunPartial {
		t.Fatalf("status = %s", res.Run.Status)
	}
}
```

Add `internal/engine/helpers_test.go`:

```go
package engine

import (
	"testing"

	"github.com/cole/fetch/internal/pipeline"
)

func newTempRepo(t *testing.T) *pipeline.Repository {
	t.Helper()
	return pipeline.NewRepository(t.TempDir())
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/engine/`
Expected: FAIL — `undefined: New`, etc.

- [ ] **Step 3: Write `internal/engine/event.go`**

```go
// Package engine executes pipeline plans: dependency-ordered step execution,
// run/trace recording, progress events, and bounded agent fallback.
package engine

import "github.com/cole/fetch/internal/core"

// EventType categorizes a progress event streamed during a run.
type EventType string

const (
	EventRunStarted   EventType = "run_started"
	EventStepStarted  EventType = "step_started"
	EventStepRetry    EventType = "step_retry"
	EventFallback     EventType = "fallback"
	EventStepFinished EventType = "step_finished"
	EventRunFinished  EventType = "run_finished"
)

// Event is a single progress update the engine emits over a channel.
type Event struct {
	RunID   string
	StepID  string
	Type    EventType
	Message string
	Status  string
}

// Fallback decision actions.
const (
	ActionAdapt = "adapt"
	ActionSkip  = "skip"
	ActionAbort = "abort"
)

// Decision is the Replanner's verdict on a failed step.
type Decision struct {
	Action string
	Step   core.Step // for ActionAdapt: the patched step
	Reason string
}

// ReplanRequest is the context handed to the Replanner on failure.
type ReplanRequest struct {
	Pipeline core.Pipeline
	Step     core.Step
	Params   map[string]any
	Attempt  int
	Err      string
}
```

`Replanner` itself is declared in `engine.go` (Step 4), because its method needs `context.Context`. `event.go` ends at the `ReplanRequest` struct above.

- [ ] **Step 4: Write `internal/engine/engine.go`**

```go
package engine

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cole/fetch/internal/agent"
	"github.com/cole/fetch/internal/config"
	"github.com/cole/fetch/internal/core"
	"github.com/cole/fetch/internal/pipeline"
	"github.com/cole/fetch/internal/providers/artifacts"
	"github.com/cole/fetch/internal/providers/fetch"
	"github.com/cole/fetch/internal/providers/search"
	"github.com/cole/fetch/internal/providers/store"
)

// Replanner decides how to recover from a failed step (nil ⇒ no fallback).
type Replanner interface {
	Replan(ctx context.Context, req ReplanRequest) (Decision, error)
}

// Revision is a self-heal candidate: a step that only succeeded after a
// run-time adaptation, offered for promotion into the pipeline definition.
type Revision struct {
	StepID   string
	Original core.Step
	Adapted  core.Step
	RunID    string
}

// RunResult is the outcome of a single Run.
type RunResult struct {
	Run        core.Run
	Traces     []core.StepTrace
	Candidates []Revision
}

// Deps are the engine's collaborators and tunables.
type Deps struct {
	Config      config.Config
	LLM         agent.LLM
	Search      search.Search
	Fetcher     fetch.Fetcher
	Artifacts   artifacts.Store
	Store       store.Store
	Replanner   Replanner             // optional
	Repo        *pipeline.Repository  // optional, required for AutoPromote
	MaxRetries  int                   // default 2
	AutoPromote bool
	Now         func() time.Time      // optional
	IDGen       func() string         // optional
}

// Engine executes pipelines.
type Engine struct {
	d Deps
}

// New builds an Engine, filling in defaults for unset tunables.
func New(d Deps) *Engine {
	if d.MaxRetries == 0 {
		d.MaxRetries = 2
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.IDGen == nil {
		d.IDGen = defaultIDGen()
	}
	return &Engine{d: d}
}

func defaultIDGen() func() string {
	var mu sync.Mutex
	var n int
	return func() string {
		mu.Lock()
		n++
		id := n
		mu.Unlock()
		return fmt.Sprintf("run-%d-%d", time.Now().UnixNano(), id)
	}
}

type runState struct {
	pipeline core.Pipeline
	run      *core.Run
	scope    pipeline.Scope
	events   chan<- Event
}

func (e *Engine) emit(rs *runState, ev Event) {
	ev.RunID = rs.run.ID
	if rs.events != nil {
		rs.events <- ev
	}
}

// Run executes a validated pipeline against input, recording runs/traces and
// streaming events (events may be nil). It returns once the plan finishes or a
// step aborts.
func (e *Engine) Run(ctx context.Context, p core.Pipeline, input map[string]any, events chan<- Event) (RunResult, error) {
	if err := pipeline.Validate(p); err != nil {
		return RunResult{}, err
	}
	order, err := pipeline.TopoOrder(p)
	if err != nil {
		return RunResult{}, err
	}
	run := core.Run{
		ID:         e.d.IDGen(),
		PipelineID: p.ID,
		Input:      input,
		Status:     core.RunRunning,
		StartedAt:  e.d.Now(),
	}
	rs := &runState{
		pipeline: p,
		run:      &run,
		scope:    pipeline.Scope{Input: input, Steps: map[string]map[string]any{}},
		events:   events,
	}
	_ = e.d.Store.RecordRun(ctx, run)
	e.emit(rs, Event{Type: EventRunStarted})

	var (
		traces     []core.StepTrace
		candidates []Revision
		runErr     error
	)
	status := core.RunOK
	for _, step := range order {
		e.emit(rs, Event{Type: EventStepStarted, StepID: step.ID, Message: step.Name})
		trace, revs, err := e.execStep(ctx, rs, step)
		traces = append(traces, trace)
		candidates = append(candidates, revs...)
		_ = e.d.Store.RecordTrace(ctx, trace)
		e.emit(rs, Event{Type: EventStepFinished, StepID: step.ID, Status: trace.Status})
		if err != nil {
			status = core.RunFailed
			runErr = err
			break
		}
		if trace.Status == "partial" && status == core.RunOK {
			status = core.RunPartial
		}
	}

	run.Status = status
	run.FinishedAt = e.d.Now()
	_ = e.d.Store.RecordRun(ctx, run)
	e.emit(rs, Event{Type: EventRunFinished, Status: string(status)})

	if e.d.AutoPromote && e.d.Repo != nil && len(candidates) > 0 {
		promoted := applyRevisions(p, candidates)
		promoted.Version++
		_ = e.d.Repo.Save(promoted)
	}

	return RunResult{Run: run, Traces: traces, Candidates: candidates}, runErr
}

// execStep runs one step, applying bounded fallback on failure. It returns the
// step's trace, any self-heal candidate revisions, and a terminal error (only
// when the step ultimately fails/aborts).
func (e *Engine) execStep(ctx context.Context, rs *runState, step core.Step) (core.StepTrace, []Revision, error) {
	trace := core.StepTrace{RunID: rs.run.ID, StepID: step.ID}
	current := step
	adapted := false

	for attempt := 0; ; attempt++ {
		params, rerr := pipeline.Resolve(current.Params, rs.scope)
		var (
			res stepResult
			err error
		)
		if rerr != nil {
			err = rerr
		} else {
			res, err = e.rawExec(ctx, rs, current, params)
		}
		if err == nil {
			rs.scope.Steps[step.ID] = res.output
			trace.Status = "ok"
			trace.Tokens += res.tokens
			trace.ArtifactRefs = res.artifactRefs
			trace.OutputSummary = res.summary
			trace.FallbackUsed = adapted
			var revs []Revision
			if adapted {
				revs = append(revs, Revision{StepID: step.ID, Original: step, Adapted: current, RunID: rs.run.ID})
			}
			return trace, revs, nil
		}

		trace.Error = err.Error()
		e.emit(rs, Event{Type: EventStepRetry, StepID: step.ID, Message: err.Error()})

		if e.d.Replanner == nil || attempt >= e.d.MaxRetries {
			trace.Status = "failed"
			return trace, nil, err
		}
		decision, derr := e.d.Replanner.Replan(ctx, ReplanRequest{
			Pipeline: rs.pipeline, Step: current, Params: params, Attempt: attempt + 1, Err: err.Error(),
		})
		if derr != nil {
			trace.Status = "failed"
			return trace, nil, err
		}
		trace.FallbackUsed = true
		e.emit(rs, Event{Type: EventFallback, StepID: step.ID, Message: decision.Action + ": " + decision.Reason})
		switch decision.Action {
		case ActionAdapt:
			current = decision.Step
			adapted = true
			continue
		case ActionSkip:
			trace.Status = "partial"
			return trace, nil, nil
		default: // ActionAbort or unknown
			trace.Status = "failed"
			return trace, nil, err
		}
	}
}
```

`applyRevisions` and `stepResult`/`rawExec` are defined in sibling files (`convert.go`, `executors.go`) in the same `engine` package; `engine.go` calls them without re-declaring them.

- [ ] **Step 5: Write `internal/engine/executors.go`**

```go
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/cole/fetch/internal/core"
	"github.com/cole/fetch/internal/providers/fetch"
	"github.com/cole/fetch/internal/providers/search"
)

// stepResult is what a step executor produces.
type stepResult struct {
	output       map[string]any
	artifactRefs []string
	tokens       int
	summary      string
}

func (e *Engine) rawExec(ctx context.Context, rs *runState, step core.Step, params map[string]any) (stepResult, error) {
	switch step.Type {
	case core.StepSearch:
		return e.execSearch(ctx, rs, step, params)
	case core.StepFetch:
		return e.execFetch(ctx, rs, step, params)
	case core.StepExtract:
		return e.execExtract(ctx, rs, step, params)
	case core.StepTransform:
		return e.execTransform(ctx, rs, step, params)
	case core.StepStore:
		return e.execStore(ctx, rs, step, params)
	default:
		return stepResult{}, fmt.Errorf("unknown step type %q", step.Type)
	}
}

func (e *Engine) execSearch(ctx context.Context, rs *runState, step core.Step, params map[string]any) (stepResult, error) {
	q, _ := params["query"].(string)
	if q == "" {
		return stepResult{}, errors.New("search: empty query")
	}
	max := e.d.Config.Search.MaxResults
	if m, ok := toInt(params["max_results"]); ok && m > 0 {
		max = m
	}
	results, err := e.d.Search.Search(ctx, q, search.Options{MaxResults: max})
	if err != nil {
		return stepResult{}, err
	}
	if len(results) == 0 {
		return stepResult{}, fmt.Errorf("search: no results for %q", q)
	}
	urls := make([]string, 0, len(results))
	for _, r := range results {
		urls = append(urls, r.URL)
	}
	blob, _ := json.Marshal(results)
	ref, _ := e.d.Artifacts.Put(ctx, rs.run.ID, step.ID, blob, "json")
	return stepResult{
		output:       map[string]any{"results": results, "urls": urls, "count": len(results)},
		artifactRefs: []string{ref},
		summary:      fmt.Sprintf("%d results for %q", len(results), q),
	}, nil
}

func (e *Engine) execFetch(ctx context.Context, rs *runState, step core.Step, params map[string]any) (stepResult, error) {
	urls, err := toStringSlice(params["urls"])
	if err != nil {
		return stepResult{}, fmt.Errorf("fetch: %w", err)
	}
	method := fetch.MethodHTTP
	if m, ok := params["method"].(string); ok && m != "" {
		method = fetch.Method(m)
	}
	var (
		pages []fetch.Page
		refs  []string
	)
	for _, u := range urls {
		page, err := e.d.Fetcher.Fetch(ctx, u, method)
		if err != nil {
			continue
		}
		if page.StatusCode < 200 || page.StatusCode >= 300 {
			continue // gate on status before trusting Text (final-review item)
		}
		ref, _ := e.d.Artifacts.Put(ctx, rs.run.ID, step.ID, page.Raw, artifactExt(page.ContentType))
		refs = append(refs, ref)
		pages = append(pages, page)
	}
	if len(pages) == 0 {
		return stepResult{}, errors.New("fetch: no pages fetched")
	}
	return stepResult{
		output:       map[string]any{"pages": pages, "count": len(pages)},
		artifactRefs: refs,
		summary:      fmt.Sprintf("fetched %d/%d", len(pages), len(urls)),
	}, nil
}

func (e *Engine) execTransform(_ context.Context, _ *runState, _ core.Step, params map[string]any) (stepResult, error) {
	rows := toRows(params["rows"])
	op, _ := params["op"].(string)
	switch op {
	case "dedup":
		by, _ := toStringSlice(params["by"])
		rows = dedupRows(rows, by)
	case "limit":
		if n, ok := toInt(params["n"]); ok && n >= 0 && n < len(rows) {
			rows = rows[:n]
		}
	case "", "passthrough":
		// no-op
	default:
		return stepResult{}, fmt.Errorf("transform: unknown op %q", op)
	}
	return stepResult{output: map[string]any{"rows": rows}, summary: fmt.Sprintf("%d rows", len(rows))}, nil
}

func (e *Engine) execStore(ctx context.Context, rs *runState, _ core.Step, params map[string]any) (stepResult, error) {
	rows := toRows(params["rows"])
	if err := e.d.Store.EnsureTable(ctx, rs.pipeline.ID, rs.pipeline.Schema); err != nil {
		return stepResult{}, fmt.Errorf("store: ensure table: %w", err)
	}
	if err := e.d.Store.AppendRows(ctx, rs.pipeline.ID, rs.pipeline.Schema, rs.run.ID, rows); err != nil {
		return stepResult{}, fmt.Errorf("store: append: %w", err)
	}
	return stepResult{output: map[string]any{"stored": len(rows)}, summary: fmt.Sprintf("stored %d rows", len(rows))}, nil
}

// execExtract is wired in Task 5; this placeholder makes the search/fetch/
// transform/store path testable now. Task 5 deletes this method and adds the
// real one in extract.go.
func (e *Engine) execExtract(_ context.Context, _ *runState, _ core.Step, _ map[string]any) (stepResult, error) {
	return stepResult{}, errors.New("extract executor not yet wired (engine Task 5)")
}
```

This `executors.go` imports exactly: `context`, `encoding/json`, `errors`, `fmt`, `core`, `fetch`, `search` — all used. No `config` or `pipeline` import here.

- [ ] **Step 6: Write `internal/engine/convert.go`**

```go
package engine

import (
	"encoding/json"
	"strings"

	"github.com/cole/fetch/internal/core"
)

func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}

// toStringSlice accepts []string, []any of strings, or a single string.
func toStringSlice(v any) ([]string, error) {
	switch t := v.(type) {
	case nil:
		return nil, nil
	case []string:
		return t, nil
	case string:
		return []string{t}, nil
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			s, ok := e.(string)
			if !ok {
				return nil, errInvalidStringList
			}
			out = append(out, s)
		}
		return out, nil
	default:
		return nil, errInvalidStringList
	}
}

// toRows accepts []map[string]any or []any of map[string]any.
func toRows(v any) []map[string]any {
	switch t := v.(type) {
	case []map[string]any:
		return t
	case []any:
		out := make([]map[string]any, 0, len(t))
		for _, e := range t {
			if m, ok := e.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	default:
		return nil
	}
}

func artifactExt(contentType string) string {
	switch {
	case strings.Contains(contentType, "html"):
		return "html"
	case strings.Contains(contentType, "json"):
		return "json"
	default:
		return "txt"
	}
}

func dedupRows(rows []map[string]any, by []string) []map[string]any {
	seen := map[string]bool{}
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		key := rowKey(r, by)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, r)
	}
	return out
}

func rowKey(r map[string]any, by []string) string {
	if len(by) == 0 {
		b, _ := json.Marshal(r)
		return string(b)
	}
	parts := make([]string, len(by))
	for i, k := range by {
		b, _ := json.Marshal(r[k])
		parts[i] = string(b)
	}
	return strings.Join(parts, "\x00")
}

func applyRevisions(p core.Pipeline, revs []Revision) core.Pipeline {
	out := p
	out.Plan = make([]core.Step, len(p.Plan))
	copy(out.Plan, p.Plan)
	for _, rev := range revs {
		for i, s := range out.Plan {
			if s.ID == rev.StepID {
				out.Plan[i] = rev.Adapted
			}
		}
	}
	return out
}

var errInvalidStringList = &engineError{"expected a list of strings"}

type engineError struct{ msg string }

func (e *engineError) Error() string { return e.msg }
```

NOTE: remove the placeholder `applyRevisions` block from `engine.go` (Step 4) — this `convert.go` version with `[]Revision` is the real one. After writing all four engine files, run `gofmt`/the compiler and delete any leftover placeholder symbols or unused imports the notes flagged so the package builds clean.

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go test ./internal/engine/ && go vet ./internal/engine/`
Expected: PASS (all engine_test.go cases), vet clean.
If the compiler reports an unused import or duplicate `applyRevisions`, resolve per the notes above (delete placeholders / unused imports), then re-run.

- [ ] **Step 8: Commit**

```bash
git add internal/engine/event.go internal/engine/engine.go internal/engine/executors.go internal/engine/convert.go internal/engine/engine_test.go internal/engine/helpers_test.go
git commit -m "feat: engine orchestration, deterministic executors, fallback mechanism"
```

---

### Task 5: Extract executor (LLM-backed)

**Files:**
- Create: `internal/engine/extract.go`
- Modify: `internal/engine/executors.go` (replace the `execExtract` placeholder body; remove the `config` placeholder `var _`)
- Create: `internal/engine/extract_test.go`

**Interfaces:**
- Consumes: `agent.LLM`, `agent.ChatRequest/Message`, `config.RoleExtract`, `core.Field/FieldType`, `fetch.Page`, `search.Result`.
- Produces: a working `extract` step — builds a JSON schema from `pipeline.Schema`, gathers source text from prior steps, calls `LLM.Chat` with `Format`, parses `{"rows":[...]}`, coerces values to the schema's field types. Helpers: `buildExtractSchema([]core.Field) json.RawMessage`, `gatherExtractText(params, scope) string`, `parseRows(content, []core.Field) ([]map[string]any, error)`.

- [ ] **Step 1: Write the failing test**

Create `internal/engine/extract_test.go`:

```go
package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cole/fetch/internal/agent"
	"github.com/cole/fetch/internal/core"
	"github.com/cole/fetch/internal/providers/artifacts"
	"github.com/cole/fetch/internal/providers/fetch"
	"github.com/cole/fetch/internal/providers/store"
)

func TestBuildExtractSchema(t *testing.T) {
	raw := buildExtractSchema([]core.Field{
		{Name: "part", Type: core.FieldString},
		{Name: "price", Type: core.FieldFloat},
		{Name: "qty", Type: core.FieldInt},
	})
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("schema not valid json: %v", err)
	}
	if m["type"] != "object" {
		t.Fatalf("root type = %v", m["type"])
	}
	// drill: properties.rows.items.properties.price.type == "number"
	props := m["properties"].(map[string]any)
	rows := props["rows"].(map[string]any)
	items := rows["items"].(map[string]any)
	iprops := items["properties"].(map[string]any)
	if iprops["price"].(map[string]any)["type"] != "number" {
		t.Fatalf("price type = %v", iprops["price"])
	}
	if iprops["qty"].(map[string]any)["type"] != "integer" {
		t.Fatalf("qty type = %v", iprops["qty"])
	}
}

func TestExtractExecutorParsesRows(t *testing.T) {
	llm := &agent.FakeLLM{Responses: []agent.ChatResponse{{
		Content:          `{"rows":[{"part":"A1","price":12.5},{"part":"B2","price":3}]}`,
		PromptTokens:     10,
		CompletionTokens: 5,
	}}}
	ff := &fetch.FakeFetcher{Pages: map[string]fetch.Page{
		"https://x": {URL: "https://x", StatusCode: 200, ContentType: "text/html", Raw: []byte("<html>p</html>"), Text: "part A1 costs 12.5"},
	}}
	srch := &queryAwareSearch{}
	fs := store.NewFakeStore()
	e := New(fixedDeps(Deps{LLM: llm, Search: srch, Fetcher: ff, Store: fs, Artifacts: artifacts.NewFakeArtifacts()}))
	p := core.Pipeline{
		ID:     "xp",
		Schema: []core.Field{{Name: "part", Type: core.FieldString}, {Name: "price", Type: core.FieldFloat}},
		Plan: []core.Step{
			{ID: "search", Type: core.StepSearch, Params: map[string]any{"query": "good"}},
			{ID: "fetch", Type: core.StepFetch, DependsOn: []string{"search"}, Params: map[string]any{"urls": "{{steps.search.urls}}"}},
			{ID: "extract", Type: core.StepExtract, DependsOn: []string{"fetch"}, Params: map[string]any{"pages": "{{steps.fetch.pages}}"}},
			{ID: "store", Type: core.StepStore, DependsOn: []string{"extract"}, Params: map[string]any{"rows": "{{steps.extract.rows}}"}},
		},
	}
	res, err := e.Run(context.Background(), p, nil, nil)
	if err != nil {
		t.Fatalf("run: %v; traces=%+v", err, res.Traces)
	}
	if res.Run.Status != core.RunOK {
		t.Fatalf("status = %s", res.Run.Status)
	}
	if len(fs.Rows["xp"]) != 2 {
		t.Fatalf("stored rows = %d", len(fs.Rows["xp"]))
	}
	// the LLM was asked with a JSON-schema format and saw the page text.
	if len(llm.Calls) != 1 {
		t.Fatalf("llm calls = %d", len(llm.Calls))
	}
	if llm.Calls[0].Format == nil {
		t.Fatal("expected structured-output Format on the extract call")
	}
	joined := llm.Calls[0].Messages[len(llm.Calls[0].Messages)-1].Content
	if !strings.Contains(joined, "part A1 costs 12.5") {
		t.Fatalf("page text not in prompt: %q", joined)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/engine/ -run Extract`
Expected: FAIL — `undefined: buildExtractSchema`, and the run fails with the placeholder error.

- [ ] **Step 3: Replace the `execExtract` placeholder in `internal/engine/executors.go`**

Delete the placeholder `execExtract` method from `executors.go` (the real one lives in `extract.go`, next step). `executors.go`'s imports do not change — it still uses `errors` in `execSearch`/`execFetch`. `rawExec` already dispatches `core.StepExtract` to `e.execExtract`, which now resolves to the real method in `extract.go`.

- [ ] **Step 4: Write `internal/engine/extract.go`**

```go
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
```

This `extract.go` imports exactly: `context`, `encoding/json`, `fmt`, `strings`, `agent`, `config`, `core`, `fetch`, `search` — all used.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/engine/ && go vet ./internal/engine/`
Expected: PASS (including the new extract cases and all Task 4 cases), vet clean.

- [ ] **Step 6: Commit**

```bash
git add internal/engine/extract.go internal/engine/extract_test.go internal/engine/executors.go
git commit -m "feat: LLM-backed extract executor with schema-constrained output"
```

---

### Task 6: End-to-end run + `fetch run` CLI

**Files:**
- Create: `internal/engine/e2e_test.go`
- Create: `internal/cli/cli.go`
- Create: `internal/cli/cli_test.go`
- Modify: `cmd/fetch/main.go`

**Interfaces:**
- Consumes: everything above; constructs real providers from `config.Config`.
- Produces:
  - `internal/engine/e2e_test.go` — a `search → fetch → extract → transform → store` run end to end against fakes + `httptest`, asserting rows land and traces/artifacts are recorded.
  - `internal/cli` — `func Run(args []string, stdout, stderr io.Writer) int` implementing the `fetch run <pipeline.json> [--input k=v ...]` command: load+validate the pipeline JSON, build an Engine with real providers from config, run it, print run status + stored row count. `cli_test.go` tests arg parsing + the `--input` parser (`parseInputs([]string) (map[string]any, error)`) without invoking the network.
  - `cmd/fetch/main.go` delegates to `cli.Run(os.Args[1:], os.Stdout, os.Stderr)`.

- [ ] **Step 1: Write the failing e2e test**

Create `internal/engine/e2e_test.go`:

```go
package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cole/fetch/internal/agent"
	"github.com/cole/fetch/internal/config"
	"github.com/cole/fetch/internal/core"
	"github.com/cole/fetch/internal/providers/artifacts"
	"github.com/cole/fetch/internal/providers/fetch"
	"github.com/cole/fetch/internal/providers/search"
	"github.com/cole/fetch/internal/providers/store"
)

// staticSearch returns one URL pointing at the test HTTP server.
type staticSearch struct{ url string }

func (s staticSearch) Search(_ context.Context, _ string, _ search.Options) ([]search.Result, error) {
	return []search.Result{{Title: "Doc", URL: s.url, Content: "snippet"}}, nil
}

func TestEndToEndSearchFetchExtractStore(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><article><h1>Parts</h1><p>Cross reference XYZ-999 for part A1.</p></article></body></html>`))
	}))
	defer srv.Close()

	llm := &agent.FakeLLM{Responses: []agent.ChatResponse{{
		Content: `{"rows":[{"part":"A1","cross_ref":"XYZ-999"}]}`,
	}}}
	fs := store.NewFakeStore()
	e := New(fixedDeps(Deps{
		Config:    config.Default(),
		LLM:       llm,
		Search:    staticSearch{url: srv.URL},
		Fetcher:   fetch.NewHTTP("test-agent", 10, 1<<20),
		Artifacts: artifacts.NewFakeArtifacts(),
		Store:     fs,
	}))
	p := core.Pipeline{
		ID:     "xref",
		Schema: []core.Field{{Name: "part", Type: core.FieldString}, {Name: "cross_ref", Type: core.FieldString}},
		Plan: []core.Step{
			{ID: "search", Type: core.StepSearch, Params: map[string]any{"query": "{{input.part}}"}},
			{ID: "fetch", Type: core.StepFetch, DependsOn: []string{"search"}, Params: map[string]any{"urls": "{{steps.search.urls}}"}},
			{ID: "extract", Type: core.StepExtract, DependsOn: []string{"fetch"}, Params: map[string]any{"pages": "{{steps.fetch.pages}}"}},
			{ID: "transform", Type: core.StepTransform, DependsOn: []string{"extract"}, Params: map[string]any{"op": "dedup", "rows": "{{steps.extract.rows}}"}},
			{ID: "store", Type: core.StepStore, DependsOn: []string{"transform"}, Params: map[string]any{"rows": "{{steps.transform.rows}}"}},
		},
	}
	res, err := e.Run(context.Background(), p, map[string]any{"part": "A1"}, nil)
	if err != nil {
		t.Fatalf("run: %v; traces=%+v", err, res.Traces)
	}
	if res.Run.Status != core.RunOK {
		t.Fatalf("status = %s", res.Run.Status)
	}
	rows := fs.Rows["xref"]
	if len(rows) != 1 || rows[0]["cross_ref"] != "XYZ-999" {
		t.Fatalf("rows = %+v", rows)
	}
	if len(res.Traces) != 5 {
		t.Fatalf("expected 5 traces, got %d", len(res.Traces))
	}
}
```

- [ ] **Step 2: Run it to verify it fails, then passes**

Run: `go test ./internal/engine/ -run EndToEnd`
Expected: this should PASS already if Tasks 4–5 are correct (it exercises only existing code). If it fails, fix the engine per the failure before proceeding. Treat a failure here as a real defect in Tasks 4–5, not the test.

- [ ] **Step 3: Write the failing CLI test**

Create `internal/cli/cli_test.go`:

```go
package cli

import (
	"bytes"
	"testing"
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
	code := Run(nil, &out, &errBuf)
	if code == 0 {
		t.Fatal("expected non-zero exit for no args")
	}
	if errBuf.Len() == 0 {
		t.Fatal("expected usage on stderr")
	}
}

func TestRunUnknownCommand(t *testing.T) {
	var out, errBuf bytes.Buffer
	if code := Run([]string{"frobnicate"}, &out, &errBuf); code == 0 {
		t.Fatal("expected non-zero exit for unknown command")
	}
}
```

- [ ] **Step 4: Run it to verify it fails**

Run: `go test ./internal/cli/`
Expected: FAIL — `undefined: Run`, `undefined: parseInputs`.

- [ ] **Step 5: Write `internal/cli/cli.go`**

```go
// Package cli is the thin command-line entry point for fetch. v1 supports
// `fetch run <pipeline.json> [--input k=v ...]`.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/cole/fetch/internal/agent"
	"github.com/cole/fetch/internal/config"
	"github.com/cole/fetch/internal/core"
	"github.com/cole/fetch/internal/engine"
	"github.com/cole/fetch/internal/pipeline"
	"github.com/cole/fetch/internal/providers/artifacts"
	"github.com/cole/fetch/internal/providers/fetch"
	"github.com/cole/fetch/internal/providers/search"
	"github.com/cole/fetch/internal/providers/store"
)

const usage = `fetch — agentic web research pipelines

Usage:
  fetch run <pipeline.json> [--input key=value ...]
`

// Run dispatches a CLI invocation and returns a process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "run":
		return runPipeline(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

func runPipeline(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	path := args[0]
	var inputArgs []string
	for i := 1; i < len(args); i++ {
		if args[i] == "--input" && i+1 < len(args) {
			inputArgs = append(inputArgs, args[i+1])
			i++
		}
	}
	input, err := parseInputs(inputArgs)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	p, err := loadPipelineFile(path)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if err := pipeline.Validate(p); err != nil {
		fmt.Fprintf(stderr, "invalid pipeline: %v\n", err)
		return 1
	}

	cfg, _ := config.Load(defaultConfigPath())
	rowStore, err := store.OpenDuckDB(defaultDBPath(cfg))
	if err != nil {
		fmt.Fprintf(stderr, "error: open store: %v\n", err)
		return 1
	}
	defer rowStore.Close()

	e := engine.New(engine.Deps{
		Config:    cfg,
		LLM:       agent.NewOllama(cfg.Ollama.BaseURL, http.DefaultClient),
		Search:    search.NewTavily(cfg.Search.BaseURL, cfg.APIKey(), http.DefaultClient),
		Fetcher:   fetch.NewHTTP(cfg.Fetch.UserAgent, cfg.Fetch.TimeoutSeconds, cfg.Fetch.MaxBytes),
		Artifacts: artifacts.NewDisk(defaultArtifactDir(cfg)),
		Store:     rowStore,
	})

	res, err := e.Run(context.Background(), p, input, nil)
	if err != nil {
		fmt.Fprintf(stderr, "run failed (%s): %v\n", res.Run.Status, err)
		return 1
	}
	fmt.Fprintf(stdout, "run %s: %s\n", res.Run.ID, res.Run.Status)
	for _, tr := range res.Traces {
		fmt.Fprintf(stdout, "  %-10s %-8s %s\n", tr.StepID, tr.Status, tr.OutputSummary)
	}
	return 0
}

func parseInputs(pairs []string) (map[string]any, error) {
	out := map[string]any{}
	for _, kv := range pairs {
		idx := strings.IndexByte(kv, '=')
		if idx < 0 {
			return nil, fmt.Errorf("invalid --input %q (want key=value)", kv)
		}
		out[kv[:idx]] = kv[idx+1:]
	}
	return out, nil
}

func loadPipelineFile(path string) (core.Pipeline, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return core.Pipeline{}, err
	}
	var p core.Pipeline
	if err := json.Unmarshal(b, &p); err != nil {
		return core.Pipeline{}, fmt.Errorf("decode pipeline: %w", err)
	}
	return p, nil
}

func defaultConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "fetch", "config.toml")
}

func defaultDBPath(cfg config.Config) string      { return filepath.Join(cfg.DataDir, "fetch.duckdb") }
func defaultArtifactDir(cfg config.Config) string { return filepath.Join(cfg.DataDir, "artifacts") }

- [ ] **Step 6: Update `cmd/fetch/main.go`**

```go
package main

import (
	"os"

	"github.com/cole/fetch/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
```

- [ ] **Step 7: Run tests + build to verify**

Run: `go test ./... && go build ./... && go vet ./...`
Expected: all packages PASS, binary builds, vet clean. (`internal/cli` tests cover arg/usage paths; the network path is exercised only when you run the binary against real services.)

- [ ] **Step 8: Manual smoke (optional, requires real services)**

Write a sample pipeline to `examples/truck-xref.json` and run it. This is a manual check, not part of `go test`:

```bash
TAVILY_API_KEY=... go run ./cmd/fetch run examples/truck-xref.json --input part=ABC123
```
Expected: prints `run <id>: ok` (or `partial`) and a per-step status table. Skip if you don't have a key / models pulled.

- [ ] **Step 9: Commit**

```bash
git add internal/engine/e2e_test.go internal/cli/ cmd/fetch/main.go
git commit -m "feat: end-to-end run path and fetch run CLI"
```

---

## Engine Definition of Done

- `go build ./...`, `go test ./...`, `go vet ./...` pass with `CGO_ENABLED=1`; `gofmt -l internal/ cmd/` is empty.
- A `search → fetch → extract → transform → store` pipeline runs end to end against fakes + `httptest`, landing rows in the store and recording a `Run` + per-step `StepTrace`s.
- Fallback is exercised: adapt+self-heal (with auto-promote bumping pipeline version), skip (run→partial), and retry-budget exhaustion (run→failed) all have passing tests.
- `fetch run <pipeline.json> --input k=v` wires real providers from config.

## Self-Review Notes

- **Spec coverage (this plan's slice):** pipeline JSON load/save ✓ (Task 1), plan validation + DAG ordering ✓ (Task 1), `{{input}}`/`{{steps}}` templating ✓ (Task 2), the deferred `FakeStore`/`FakeArtifacts` ✓ (Task 3), five step executors ✓ (Tasks 4–5), run/trace recording + streaming events ✓ (Task 4), bounded fallback + self-heal + auto-promote ✓ (Task 4), `extract` LLM structured output ✓ (Task 5), CLI-runnable against a hand-written pipeline JSON ✓ (Task 6). Deferred-from-Plan-1 review items addressed: `RecordTrace` is called exactly once per step (Task 4 `Run`); the `fetch` executor gates on `page.StatusCode` before trusting `Text` (Task 4 `execFetch`). The LLM-backed Replanner and the conversational builder remain Plan 3; the TUI remains Plan 4. Bounded-concurrency fetch is intentionally deferred (sequential is correct for v1; a worker pool is a later optimization).
- **Type consistency:** the step output/param contract (table above) is used identically in `execSearch`→`urls []string`, `execFetch` consuming `urls`, `execExtract`/`execTransform`/`execStore` consuming `rows`. `Replanner`, `Decision`, `ReplanRequest`, `Revision`, `RunResult`, `Deps`, `Event`/`EventType` are each defined once and referenced consistently.
- **Cross-file symbols (one `engine` package, compiled together):** `Replanner` is declared once in `engine.go`; `event.go` holds only `Event`/`EventType`/`Decision`/`ReplanRequest` and the action constants. `stepResult` and the executors live in `executors.go`; `applyRevisions` and the `to*`/`dedup` helpers in `convert.go`. `engine.go` calls `rawExec`/`applyRevisions` from sibling files — valid same-package references, no forward-declaration tricks. In Task 4 `executors.go` contains a one-line placeholder `execExtract` (returns an error) so the deterministic path is testable; Task 5 deletes it and adds the real `execExtract` in `extract.go`. Every file's import list is stated explicitly beneath its code block; the package ends gofmt-clean with no unused imports.
