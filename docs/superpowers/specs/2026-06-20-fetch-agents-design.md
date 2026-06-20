# Fetch Agents (Plan 3) — Design

**Date:** 2026-06-20
**Status:** Design approved (pending spec review)

This is **Plan 3 of 4** (Foundation ✓ → Engine ✓ → **Agents** → TUI). It supplies the
LLM-backed *intelligence* that the earlier plans left as seams: the conversational
pipeline **builder** (Interviewer → SchemaDesigner → Planner) and the run-time
**Replanner** that plugs into the engine's existing `engine.Replanner` interface. The
parent design lives in `docs/superpowers/specs/2026-06-18-fetch-tui-design.md`; this
document narrows that vision to a headless, fake-testable agent layer plus a thin
`fetch create` CLI. The Bubble Tea TUI remains Plan 4.

## Goal

Turn a natural-language goal into a saved, valid `core.Pipeline` through a headless
agentic interview, and let a failed run step consult an LLM for recovery — all behind
clean interfaces, unit-tested with `agent.FakeLLM` (no network in `go test ./...`), and
exercised end to end by a `fetch create` CLI that mirrors how Plan 2 delivered
`fetch run`.

## Scope

**In:**
- Four agent roles: **Interviewer**, **SchemaDesigner**, **Planner**, **Replanner**.
- A **CreateSession** orchestrating interview → draft (split design) → validate-repair →
  conversational redraft → accept (write pipeline + ensure table).
- The LLM-backed **Replanner** wired into `engine.Deps.Replanner`; `fetch run` now
  consults it on step failure.
- A `fetch create` CLI (interactive stdin/stdout interview).
- Golden-file prompt tests; FakeLLM-driven unit tests; an engine self-heal integration
  test.

**Out (deferred):**
- **Router** agent — cut from v1 (no routing decision exists in an HTTP-only,
  deterministically-orchestrated pipeline). The `config.RoleRoute` string stays so
  nothing breaks; revisit if per-step HTTP-vs-browser routing ever lands.
- TUI (Plan 4), scheduling/daemon, headless browser, multi-provider search, exports.
- Structured field-by-field draft editing (Plan 4 TUI layers it on the same redraft
  loop); v1 CLI revision is natural-language only.

## Package layout

Keep `internal/agent` as **LLM transport only** (`LLM` interface, `Ollama`, `FakeLLM`,
`ChatRequest/ChatResponse/Message`). Add two focused packages:

- **`internal/builder`** — create-time intelligence and orchestration:
  `interviewer.go`, `designer.go` (SchemaDesigner), `planner.go`, `session.go`
  (CreateSession), `prompts.go`, `types.go`, plus `*_test.go`.
- **`internal/replanner`** — `replanner.go` (+ test): the run-time `Replanner`.

**No import cycle:** `replanner` imports `engine` for `ReplanRequest`/`Decision` and
`core` for `Step`; `engine` never imports `replanner` (it receives one via `Deps`).
`builder` imports `core`, `agent`, `config`, `pipeline`, and the provider `store`
interface (for `EnsureTable` on accept) — not `engine`.

## Data types (`internal/builder/types.go`)

```
Turn   { Role string; Content string }          // Role: "user" | "assistant"

Facts  {                                          // running structured understanding
  Domain       string
  Inputs       []core.InputParam                  // args provided per run
  OutputFields []core.Field                       // desired output row shape
  SourceHints  []string                           // sites/constraints the user named
}

InterviewState {
  Goal       string
  Transcript []Turn
  Facts      Facts
  Done       bool
}

Draft {                                           // the reviewable proposal
  Pipeline core.Pipeline                          // Inputs+Schema+Plan+Name/Desc/Domain
  Notes    string                                 // designer's rationale, optional
}
```

`Facts.Inputs`/`OutputFields` reuse `core.InputParam`/`core.Field` so the draft maps
straight onto `core.Pipeline` with no parallel type.

## The interview (one LLM call per turn)

The **Interviewer** (role `interview`) is a function over conversation state. Each user
turn triggers one JSON-schema-constrained `LLM.Chat` returning:

```
interviewReply { Question string; Ready bool; Facts Facts }
```

- `Question` is the next single clarifying question (`""` when ready).
- `Facts` is the model's best current understanding, re-emitted every turn (gives the
  Plan-4 TUI a live draft for free).
- The system prompt instructs: ask **one question at a time**, gather exactly the four
  things (domain/entity, inputs-per-run, output fields, source hints), and set
  `Ready=true` once it has enough.

`CreateSession` owns the loop: append the user's message, call the Interviewer, surface
`Question`, repeat until `Ready` — **or** until the user forces finalize. The user can
end early; the accumulated `Facts` are used as-is.

## Draft generation (split + validate-repair)

On finalize, `CreateSession.draft(state)` runs two structured calls then a repair loop:

1. **SchemaDesigner** (role `schema`) → `{ Name, Description, Domain, Inputs
   []core.InputParam, Schema []core.Field }`. One call; keeps the structured output
   small and reliable on local models.
2. **Planner** (role `plan`) → `Plan []core.Step` given the finalized Schema+Inputs.
   The prompt constrains the shape to `search → fetch → extract → [transform] → store`
   with templated params (`{{input.<name>}}`, `{{steps.<id>.<field>}}`) following the
   engine's step output/param contract (search⇒`urls`, fetch⇒`pages`, extract⇒`rows`,
   etc.).
3. **Validate-repair:** assemble `core.Pipeline`, run `pipeline.Validate` +
   `pipeline.TopoOrder`. On error, re-invoke the **Planner** with the prior plan and the
   validation error appended ("your plan failed validation: <err>; return a corrected
   plan"). Bounded to **2 repair attempts** (`maxRepairs = 2`). If still invalid, return
   the error to the caller (CLI prints it; the user can comment to redraft).

Only the Planner is repaired (validation failures are plan-shape/dependency errors);
the schema is not re-derived during repair.

## Review / redraft loop

`CreateSession` exposes the `Draft` and accepts one of three caller actions:

- **accept** → `Store.EnsureTable(ctx, pipeline.ID, pipeline.Schema)` then
  `Repo.Save(pipeline)`. (Table-create + JSON write, per the parent spec.)
- **comment(text)** → re-run SchemaDesigner + Planner with the prior draft + the
  feedback as additional context, producing a new `Draft` (validate-repair again).
- **cancel** → discard.

The redraft path reuses the same designer/planner functions with an extra "revise this
draft per the user's comment" instruction; no separate agent.

## Replanner (`internal/replanner`)

`New(llm agent.LLM, cfg config.Config) *Replanner` implements the engine interface:

```
func (r *Replanner) Replan(ctx, engine.ReplanRequest) (engine.Decision, error)
```

One structured call (role `replan`) over the failure context (step, params, attempt,
error) returning `{ action: "adapt"|"skip"|"abort", reason, step? }`:

- **adapt** → `engine.Decision{Action: ActionAdapt, Step: <patched>, Reason}`. The
  prompt instructs the model to return the **same step ID and type** with adjusted
  params (e.g. broadened query, different URL); the engine re-runs it.
- **skip** → `ActionSkip` (engine marks the step partial, run → partial).
- **abort** → `ActionAbort` (engine fails the run).

The engine already owns the bounded retry loop and self-heal capture, so the Replanner
is pure decision logic. A malformed/empty model response maps to `abort` with the parse
error as the reason (fail safe, never panic).

## CLI

- **`fetch create`** — interactive interview on stdin/stdout. Prints each assistant
  question; reads a user line. Sentinels: `/done` finalizes early, `/cancel` aborts.
  After finalize, prints the draft (schema table + step list) and prompts
  **accept / comment / cancel**; a comment line redrafts. On accept, writes
  `pipelines/<id>.json` and ensures the DuckDB table. Builds real providers (Ollama LLM,
  DuckDB store, Repo) from config.
- **`fetch run`** — unchanged CLI surface; now constructs `replanner.New(...)` and passes
  it as `engine.Deps.Replanner` so failed steps consult the LLM.

The terminal read/print loop is thin and takes injected `io.Reader`/`io.Writer`;
`CreateSession` is constructed with injectable deps so tests drive the whole create flow
with `FakeLLM` + `FakeStore` + a temp `Repo`. Pure helpers (sentinel parsing, draft
rendering) are unit-tested directly. The real-network path runs only when the binary is
invoked against live Ollama.

## Testing (TDD, no network in `go test ./...`)

- **Interviewer:** FakeLLM scripted to ask N questions then set `Ready` → assert the
  loop converges and `Facts` are threaded through.
- **SchemaDesigner / Planner:** canned structured JSON → assert correct `core` types and
  that `Format` (JSON schema) is set on the request.
- **Validate-repair:** FakeLLM returns an invalid plan (e.g. unknown dep / cycle) then a
  valid one → assert the repair call fired and the final pipeline passes
  `pipeline.Validate`. A second case: still-invalid after `maxRepairs` → assert a
  surfaced error.
- **Redraft:** accept vs comment vs cancel each exercised; accept asserts `EnsureTable`
  + `Repo.Save` happened.
- **Replanner:** one test per action (adapt/skip/abort) decoding to the right
  `engine.Decision`; a malformed-response test → `abort`. Plus an **engine integration
  test**: real `replanner.New(FakeLLM…)` injected into an `engine.New(...)` run whose
  first step fails, asserting adapt → self-heal candidate (reusing Plan 2's engine test
  patterns).
- **Golden-file prompt tests:** render each agent's system/user prompt for a fixed input
  and compare to a checked-in golden (per the parent spec).
- **Determinism:** `CreateSession` takes `Now func() time.Time` and `IDGen func()
  string`; tests inject fixed ones. Pipeline ID derives from a slug of the name with
  `IDGen` as fallback/uniqueness.

## Definition of Done

- `go build ./...`, `go test ./...`, `go vet ./...` pass with `CGO_ENABLED=1`;
  `gofmt -l internal/ cmd/` empty.
- A scripted interview (FakeLLM) produces a draft that passes `pipeline.Validate`, and
  accept writes a `pipelines/<id>.json` + ensures a table — all in tests.
- The validate-repair loop is proven: an initially-invalid plan is corrected and saved.
- `replanner.New(...)` satisfies `engine.Replanner`; an engine run with a failing step
  recovers via LLM adapt in an integration test.
- `fetch create` runs the interview→draft→accept flow against real config/providers;
  `fetch run` injects the real Replanner.

## Self-review notes

- **Boundaries:** `internal/agent` = transport; `internal/builder` = create-time;
  `internal/replanner` = run-time repair. Each unit takes its collaborators as
  interfaces and is testable in isolation with fakes.
- **No new domain types:** `Facts`/`Draft` wrap existing `core` types; the draft is a
  `core.Pipeline`. `Decision`/`ReplanRequest` come from `engine` (Plan 2), unchanged.
- **Reliability for local models:** split design + bounded validate-repair is the
  deliberate hedge against weaker structured-output reliability; every agent call uses
  Ollama JSON-schema `Format`.
- **Engine untouched:** Plan 3 adds no engine changes — it fills the `Replanner` seam
  and consumes `pipeline`/`core`/`store` as-is. (Plan 2's deferred minors —
  `gatherExtractText` `[]any`, events blocking-send — remain Plan 4 concerns.)
