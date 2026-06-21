# Fetch TUI (Plan 4) — Design

**Date:** 2026-06-20
**Status:** Design approved (pending spec review)

This is **Plan 4 of 4** (Foundation ✓ → Engine ✓ → Agents ✓ → **TUI**). It is the
user-facing Bubble Tea terminal app that sits entirely on top of the service APIs the
first three plans built — no new domain logic, just presentation, navigation, and the
async plumbing that adapts blocking service calls to Bubble Tea's event loop. The parent
vision lives in `docs/superpowers/specs/2026-06-18-fetch-tui-design.md` (the four
screens); this document narrows it to a concrete, fake-testable implementation.

## Goal

Ship a four-screen TUI — **Home/list**, **Create-interview**, **Run**, **Results** —
that lets the author build a pipeline by conversation, run it on inputs while watching
live progress, and browse results and run history, all driven by the existing
`builder.CreateSession`, `engine.Engine`, `pipeline.Repository`, and `store.Store`
APIs. Every screen is unit-tested by driving `Update` directly with synthesized
messages and asserting on model state + `View()` output, with fakes standing in for the
LLM/engine/store (no network in `go test ./...`).

## Scope

**In:**
- A new `internal/tui` package: a root model + one sub-model per screen, global
  keybindings, window-size handling, and an async command/message layer.
- Four screens: Home/list, Create, Run, Results (full drill-down).
- Launch via `fetch` with **no args**; the headless `fetch run` / `fetch create`
  subcommands stay unchanged for scripting.
- Three small, justified additions to existing packages (see "Service additions").
- Promote-revision: after a run whose self-heal produced candidate adaptations, offer to
  write the adaptation back into the saved pipeline.
- Direct `Update`/`View` unit tests with fakes; no golden files, no network.

**Out (deferred):**
- Mouse support, color themes / config-editing UI, concurrent runs.
- Editing a saved pipeline outside the create→redraft loop.
- Surfacing the browser-fetch stub, multi-provider search, scheduling/daemon, exports.
- Persisting self-heal candidates for later promotion (promotion is a live post-run
  action only — candidates are in memory in `RunResult`, not stored).

## Architecture

**Approach: root model + screen sub-models.** One root `Model` owns a `screen` enum, a
shared `services` value, the current window size, and one sub-model per screen. The root
routes `Update`/`View` to the active sub-model and handles global keys (quit, help) and
`tea.WindowSizeMsg`. Each screen sub-model is a plain struct with an
`update(msg) (self, tea.Cmd)` method and a `view() string` method (not a full
`tea.Model` — the root is the only `tea.Model`). This keeps each screen small,
independently testable, and holdable in context at once.

```
cmd/fetch → internal/cli.Run("" → tui.Run) → tui.Model (root)
  ├─ screenHome     → homeModel
  ├─ screenCreate   → createModel
  ├─ screenRun      → runModel
  └─ screenResults  → resultsModel
        │ services{cfg, Repo, Store, newSession(), newEngine()}
        ▼
builder.CreateSession · engine.Engine · pipeline.Repository · store.Store
```

## Package layout (`internal/tui`)

- `app.go` — root `Model`, `screen` enum, `Init/Update/View`, global keys, window size,
  the `services` struct, and screen transitions.
- `home.go` — `homeModel` (pipeline list).
- `create.go` — `createModel` (interview + live facts + draft review).
- `run.go` — `runModel` (input form + live run log + promote prompt).
- `results.go` — `resultsModel` (run history → rows → trace drill-down).
- `commands.go` — async `tea.Cmd` constructors and the typed message structs they
  return (one place for the blocking→async boundary).
- `styles.go` — shared `lipgloss` styles.
- `*_test.go` — per-screen and root tests.

Entry point: `cmd/fetch/main.go` already calls `cli.Run(os.Args[1:], ...)`. `cli.Run`
gains a no-subcommand branch that calls `tui.Run(cfg-derived services)`. `tui.Run`
constructs the real services and starts `tea.NewProgram(model).Run()`.

## Dependencies

Add to `go.mod` (all pure-Go, no cgo):
- `github.com/charmbracelet/bubbletea`
- `github.com/charmbracelet/bubbles` (table, textinput, viewport)
- `github.com/charmbracelet/lipgloss`

## Services struct & injected interfaces (testability)

The root model holds:

```go
type services struct {
    cfg        config.Config
    repo       *pipeline.Repository
    store      store.Store
    newSession func() Session       // builder-backed in prod, fake in tests
    newEngine  func() EngineRunner  // engine-backed in prod, fake in tests
}

type Session interface { // satisfied by *builder.CreateSession
    Start(goal string)
    Reply(ctx context.Context, msg string) (question string, ready bool, err error)
    Facts() builder.Facts
    Finalize(ctx context.Context) (builder.Draft, error)
    Redraft(ctx context.Context, comment string) (builder.Draft, error)
    Accept(ctx context.Context, d builder.Draft) (id string, err error)
}

type EngineRunner interface { // satisfied by *engine.Engine
    Run(ctx context.Context, p core.Pipeline, input map[string]any,
        events chan<- engine.Event) (engine.RunResult, error)
}
```

`Repo` and `Store` are the real types (file-backed repo with a temp dir in tests; the
existing `store` fake in tests). The two factory funcs are the seam that keeps the LLM
and engine out of tests.

## Async / message boundary (`commands.go`)

Bubble Tea's `Update` must never block, but `Reply`, `Finalize`, `Redraft`, `Accept`,
and `engine.Run` are all synchronous (and do network I/O in prod). Each is wrapped in a
`tea.Cmd` that runs the call and returns a typed message:

```go
replyMsg     { question string; ready bool; err error }
finalizeMsg  { draft builder.Draft; err error }
redraftMsg   { draft builder.Draft; err error }
acceptMsg    { id string; err error }
eventMsg     { ev engine.Event }
runDoneMsg   { result engine.RunResult; err error }
```

**Engine event draining (the Plan-2 footgun):** `engine.Run` does a *blocking send* on
its events channel. The run is launched in a goroutine that owns the channel:

```go
events := make(chan engine.Event, 128) // buffer smooths bursts; does NOT replace draining
go func() {
    res, err := eng.Run(ctx, p, input, events)
    close(events)
    doneCh <- runDoneMsg{res, err}
}()
```

The screen issues a `waitEvent(events)` cmd that blocks on one receive and returns an
`eventMsg`; on each `eventMsg` the screen re-issues `waitEvent` (a self-perpetuating
drain) until the channel closes, then a separate `waitDone(doneCh)` cmd delivers
`runDoneMsg`. This guarantees the channel is continuously drained so the engine never
blocks.

## Screen: Home / list (`home.go`)

`bubbles/table` populated from `repo.List()` (columns: Name, Domain, #Inputs, Version,
Created). Keybindings:
- `c` → Create screen.
- `enter` / `r` → Run screen for the highlighted pipeline.
- `v` → Results screen for the highlighted pipeline.
- `d` → delete: shows an inline `delete "<name>"? (y/n)` confirm; `y` calls
  `repo.Delete(id)` and reloads the list, `n`/`esc` cancels.
- `q` / `ctrl+c` → quit.

Empty state: a hint line ("No pipelines yet — press c to create one"). Reload is a
`tea.Cmd` returning a `pipelinesLoadedMsg{[]core.Pipeline, err}` so a transient repo
error renders as a status line rather than a panic.

## Screen: Create (`create.go`)

Split layout via `lipgloss.JoinHorizontal`:
- **Left:** a `viewport` rendering the transcript (assistant questions + user replies)
  and a `textinput` for the next reply.
- **Right:** a **live facts pane** rendered from `session.Facts()` after every turn —
  Domain, Inputs (name:type, required), Output fields (name:type), Source hints.

Flow:
1. The Create screen is entered with a goal prompt (a `textinput`); on submit it calls
   `session.Start(goal)` then issues `replyCmd(session, "")` to get the first question.
2. Each user line issues `replyCmd(session, line)` → `replyMsg`. On `ready=true` (or the
   user types `/done`) it issues `finalizeCmd` → `finalizeMsg{draft}`. `/cancel` returns
   to Home.
3. **Draft review:** once a `Draft` exists, the right pane switches from live facts to
   the full draft — a schema table (`Field` name/type/description), the step list
   (`Step` id, type, depends-on), and `Notes`. The footer offers
   `[a]ccept  [c]omment  [x]cancel`.
   - `a` → `acceptCmd(session, draft)` → on success, status "saved <id>" and return Home
     (list reloads).
   - `c` → opens the reply `textinput`; the line is sent via `redraftCmd(session, line)`
     → `redraftMsg{draft}` (new draft, repeat review). Validate-repair happens inside
     the builder; a surfaced error renders as a status line and stays in review.
   - `x` → discard, return Home.

All builder calls are async cmds; while one is in flight the footer shows a spinner-less
"…thinking" status and input is disabled.

## Screen: Run (`run.go`)

1. **Input form:** one `bubbles/textinput` per `pipeline.Inputs` entry, labeled with
   name + type + (required). `tab`/`shift+tab` move between fields; `enter` on the last
   (or `ctrl+s`) submits. Required-but-empty fields block submit with an inline error.
   Values are coerced to the `InputParam.Type` (string/int/float/bool) into the
   `map[string]any` input; a coercion error is shown inline.
2. **Live run log:** on submit, launch the engine goroutine (above) and start draining.
   A `viewport` appends a line per `engine.Event`:
   - `run_started` → header with run id.
   - `step_started` → "▶ <stepID>".
   - `step_retry` / `fallback` → highlighted line (lipgloss warning style), including
     `Event.Message`.
   - `step_finished` → "✓/✗ <stepID> — <status>".
   - `run_finished` → footer.
3. **Completion:** on `runDoneMsg`, show final `RunStatus` and the stored row count
   (`len(store.ResultRows(ctx, pipelineID, result.Run.ID))`, fetched as a follow-up cmd).
   If `result.Candidates` is non-empty,
   render a **promote prompt**: "Run adapted N step(s) via fallback. Promote into saved
   pipeline? (y/n)". `y` applies each `engine.Revision` to the loaded `core.Pipeline`
   (swap the step whose `ID == rev.StepID` with `rev.Adapted`, bump `Version`),
   `repo.Save`, status "promoted, now v<N>". `n`/`esc` skips. Footer then offers
   `[v]iew results  [enter] back to home`.

The promote helper is a pure function in `tui` (it needs both `engine.Revision` and
`core.Pipeline`; `pipeline` cannot import `engine`, so it lives here):

```go
func promote(p core.Pipeline, revs []engine.Revision) core.Pipeline
// returns a copy with adapted steps swapped in and Version+1
```

## Screen: Results (`results.go`)

A three-level drill-down for the selected pipeline:
1. **Run history:** a `table` of `store.Runs(ctx, pipelineID)` — run id, status, started,
   finished, row count. `enter` drills into a run.
2. **Result rows:** a `table` of `store.ResultRows(ctx, pipelineID, runID)` — columns are
   the pipeline `Schema` fields. `esc` back to history.
3. **Trace drill-down:** `t` on a run shows `store.RunTraces(ctx, runID)` — per-step
   status, input/output summaries, `Tokens`, `FallbackUsed`, `Error`, and
   `ArtifactRefs`. `esc` back.

Loading each level is a `tea.Cmd` returning a typed loaded-message so store errors are
status lines, not panics.

## Service additions (small, in existing packages)

1. **`builder.CreateSession.Facts() Facts`** — returns the session's accumulated
   `Facts` (domain/inputs/output-fields/source-hints) so the Create screen can render a
   live pane mid-interview. Pure getter over existing internal state; no behavior change.

2. **`store.Store` read methods** (added to the interface; real DuckDB impl wraps the
   existing `Query`; the in-memory fake gets simple implementations):
   ```go
   Runs(ctx context.Context, pipelineID string) ([]core.Run, error)
   ResultRows(ctx context.Context, pipelineID string, runID string) ([]map[string]any, error)
   RunTraces(ctx context.Context, runID string) ([]core.StepTrace, error)
   ```
   This keeps raw SQL out of the TUI and makes the Results screen fully fakeable.
   `engine.Deps.Store` and `builder.SessionDeps.Store` already take the interface, so
   widening it is transparent to those consumers (they don't call the new methods).

3. **`tui.promote(p, revs)`** — pure helper described above (no cross-package import
   issue since it lives in `tui`).

## Testing (TDD, no network in `go test ./...`)

- **Root/app:** screen transitions on global keys; `WindowSizeMsg` propagation; quit.
- **Home:** list renders from a fake repo; `d`→`y` calls `Delete` and reloads;
  empty-state hint; navigation keys set the right `screen`.
- **Create:** a `fakeSession` (scripted `Reply` returns N questions then `ready`, canned
  `Facts`, canned `Draft`) drives the full loop: `Start`→first question→replies→finalize
  →review; assert the live facts pane reflects `Facts()`, that `/done` finalizes, that
  `a` calls `Accept` and returns Home, that `c` calls `Redraft`. Assert async: feeding
  `replyMsg`/`finalizeMsg` advances state.
- **Run:** required-field validation blocks submit; type coercion; feeding a scripted
  sequence of `eventMsg`s renders the log with fallback lines highlighted; `runDoneMsg`
  with non-empty `Candidates` shows the promote prompt and `y` calls `repo.Save` with an
  incremented `Version` (assert via a temp repo). `promote()` unit-tested directly
  (step swapped, version bumped, untouched steps preserved).
- **Results:** a fake store returns canned runs/rows/traces; assert each drill level
  renders and `esc` pops back.
- **Store read methods:** unit-test the real DuckDB impl (CGO, local, no network) round-
  trips `RecordRun`/`AppendRows`/`RecordTrace` → `Runs`/`ResultRows`/`RunTraces`; and the
  fake's implementations match.
- **commands.go:** the `waitEvent` self-re-issue drains a channel to completion without
  blocking a producer (a test producer sends > buffer cap events).

## Definition of Done

- `go build ./...`, `go test ./...`, `go vet ./...` pass with `CGO_ENABLED=1`;
  `gofmt -l internal/ cmd/` empty; `go test -race ./internal/tui/...` clean.
- `fetch` (no args) launches the TUI; `fetch run` / `fetch create` still work headless.
- All four screens implemented and unit-tested with fakes; no network in the test suite.
- `CreateSession.Facts()` and the three `Store` read methods exist with fake parity.
- A run that produces self-heal candidates can be promoted into the saved pipeline
  (proven in a test against a temp repo).

## Self-review notes

- **Boundaries:** `internal/tui` is presentation + async glue only. It depends on
  `builder`, `engine`, `pipeline`, `store`, `core`, `config`, `agent` (for the prod
  factories) — and adds *no* domain logic except the pure `promote` helper. Screens
  communicate with services exclusively through the two injected interfaces plus the
  Repo/Store, so every screen is testable with fakes.
- **No import cycles:** `tui` imports everything below it; nothing imports `tui`.
  `promote` lives in `tui` precisely because it bridges `engine.Revision` and
  `core.Pipeline` (and `pipeline` must not import `engine`).
- **The two Plan-2 footguns are handled here:** the blocking events channel is drained by
  a self-perpetuating `waitEvent` cmd over a buffered channel; `gatherExtractText`'s
  missing `[]any` fallback is an engine concern unrelated to the TUI and stays deferred
  (the TUI never calls it).
- **Promotion is live, not historical:** self-heal candidates live in `RunResult`
  (in-memory), so promotion is offered on the Run screen at completion. The Results
  screen shows `FallbackUsed` historically but does not promote — avoiding a candidate-
  persistence layer this plan doesn't need.
- **Reliability:** every blocking/network call crosses the `commands.go` boundary as a
  `tea.Cmd`, so `Update` stays non-blocking and the UI never freezes on a slow local
  model.
