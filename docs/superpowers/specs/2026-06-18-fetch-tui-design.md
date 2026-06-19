# Fetch — Agentic Web Research/Scraping Pipeline TUI

**Date:** 2026-06-18
**Status:** Design approved (not yet committed — pending user)

## Summary

`fetch` is a Go terminal UI for building and running **agentic web research/scraping
pipelines** in natural language, backed by local Ollama models. You describe a goal
("build a truck-part cross-reference pipeline", "get NFL running-back gamelogs for the
last 3 seasons"); an agent interviews you, then generates a reusable, repeatable
**pipeline** (inputs + output schema + an ordered plan). You run a pipeline on an
input and results land in DuckDB (structured rows) plus an on-disk artifact store (raw
fetched bytes). The system is domain-agnostic.

A pipeline is **data, not code**: an agent-generated declarative artifact the engine
executes deterministically, calling the LLM only for **extraction** (HTML → structured
rows) and **fallback re-planning** (when a step fails).

## Goals & context

- **Primary use:** a personal power tool for the author, with architecture clean
  enough to grow into a product for others later. Bias toward capability + clear,
  testable boundaries; defer polish.
- **Execution model:** **hybrid** — a saved deterministic plan is the happy path; a
  live agent re-plans only when a step fails (robust to web drift).
- **Web access:** per-step choice of HTTP vs headless browser (v1: **HTTP only**;
  browser behind the interface as a stub).
- **Search:** pluggable behind a `Search` interface; **Tavily** is the default backend.
- **Storage:** **DuckDB** (one table per pipeline) for structured rows **plus** a raw
  on-disk **artifact store** so extraction can be re-run without re-fetching.
- **Models:** **role → model** mapping, configurable.

## Architecture

Five layers, each behind a clean interface so external dependencies are swappable and
testable with fakes.

```
TUI layer (Bubble Tea)
  Home/list · Create-interview · Run · Results browser
        │  app service API + streaming channels
Agent layer (Ollama)
  roles→models registry · prompts · structured-output
  Interviewer · Planner · SchemaDesigner · Extractor · Replanner(fallback) · Router
        │
Pipeline model            │  Execution engine
  Pipeline·Plan·Step·      │  step executors · run records ·
  Schema·Inputs            │  agent-fallback orchestration
        │
Capability providers (interfaces)
  Search(Tavily) · Fetcher(HTTP | browser-stub) · Store(DuckDB) ·
  ArtifactStore(disk) · LLM(Ollama)
```

During a run the LLM is invoked only at **extract** and **fallback re-plan**;
everything else is plain Go.

## Data model

**Pipeline** (`pipelines/<id>.json`):
```
Pipeline {
  ID, Name, Description, Domain
  Inputs   []InputParam   // run-time args, e.g. {name:"part_number", type:string, required}
  Schema   []OutputField  // {name, type, description} → DuckDB columns
  Plan     []Step         // ordered, dependency-aware
  Models   map[Role]Model // optional per-pipeline overrides of global role→model map
  CreatedAt, Version
}
```

**Step:**
```
Step {
  ID, Name
  Type      search | fetch | extract | transform | store
  Params    map[string]any   // templated: {{input.part_number}}, {{steps.search.urls}}
  DependsOn []StepID
}
```

**Run + StepTrace** (DuckDB `runs` table + traces):
```
Run { ID, PipelineID, Input, Status(running|ok|failed|partial), StartedAt, FinishedAt }
StepTrace { RunID, StepID, Status, InputSummary, OutputSummary,
            ArtifactRefs[], Tokens, Error, FallbackUsed }
```

**Artifact** (`artifacts/<run>/<step>/<hash>.{html,json}`): raw fetched bytes,
referenced by `StepTrace.ArtifactRefs` so extraction can re-run without re-fetching.

**Results table:** one DuckDB table per pipeline; columns = `Schema` fields +
`__run_id` + `__fetched_at`. Runs append rows.

**Note:** `Inputs` (what you provide to start a run) and `Schema` (the shape of each
output row) are distinct; the interview must nail both.

## Creation interview

The conversational loop that turns a goal into a saved Pipeline. Agentic — the
Interviewer decides when it has enough.

1. User describes the goal in natural language (Create screen).
2. **Interviewer** (gpt-oss:20b) asks clarifying questions **one at a time**,
   gathering exactly four things:
   - entity/domain
   - inputs provided per run
   - desired output fields
   - source hints / constraints
3. User can stop the interview at any time ("that's enough, build it").
4. **Planner + SchemaDesigner** turn the transcript into a draft: `Inputs`, `Schema`,
   `Plan`.
5. **Draft review pane** — TUI shows the proposed schema + plan in a structured,
   editable view; user tweaks fields/steps or sends it back with a comment to re-draft.
6. On confirm → create the DuckDB table from `Schema`, write `pipelines/<id>.json`.

**Decision:** the interview **converges to a structured, reviewable, editable draft**
rather than staying pure free-form chat — this makes the output trustworthy and
repeatable (you see exactly what will be scraped/stored before committing).

## Execution engine & agent fallback

The engine walks the plan in dependency order. Each step type has a deterministic
executor:

| Step | Executor | LLM? |
|------|----------|------|
| search | Tavily query (templated) → ranked URLs + snippets, saved as artifact | no |
| fetch | HTTP GET each URL → raw bytes to artifact store; readability-clean to text | no |
| extract | Extractor (qwen3-coder) given clean text + `Schema` → rows via Ollama JSON-schema-constrained output | yes |
| transform | dedup / filter / merge rows (plain Go) | no |
| store | append rows to the pipeline's DuckDB table + link artifacts | no |

**Agent fallback (hybrid).** A step "fails" when it errors, returns empty, or
extraction confidence is low. The engine invokes the **Replanner** (gpt-oss:20b) with
the failure context (step, params, error, sample of what came back). It returns:
- **adapt** — a patched step (broaden query, try a different page, escalate HTTP→browser
  *[browser is a v2 stub → reports "unavailable" for now]*),
- **skip** — mark the step partial and continue, or
- **abort** — unrecoverable.

Retries are **bounded** (default **2 per step**, configurable). Every attempt is
recorded in `StepTrace` with `FallbackUsed=true`.

**Self-healing.** When a run-time adaptation **succeeds**, the engine captures the
improved step as a **candidate revision** (before/after + the run that proved it). The
Results screen surfaces a one-click **"promote to pipeline"** that bumps
`Pipeline.Version`; a config flag enables **auto-promote** (silent self-heal).
Provenance is preserved either way.

**Concurrency:** fetch/extract over many URLs run in a bounded worker pool; the run
streams progress events to the TUI over a channel so the UI never blocks and steps tick
by live.

## TUI structure

Bubble Tea; one app-level model owns shared services and routes between four screens:

- **Home / Pipeline list** — table of pipelines; `[c]`reate `[r]`un `[v]`iew `[d]`elete.
- **Create** — split view: interview chat (left) + live draft of schema+plan (right);
  converges to the editable review pane.
- **Run** — input form generated from `Inputs`, then a live streaming run log; fallback
  interventions highlighted, errors inline.
- **Results** — per-pipeline results table (DuckDB), run history, drill into a
  `StepTrace` to see artifacts and agent interventions; promote-revision prompt lives
  here.

The engine emits progress as channel messages consumed by the Bubble Tea event loop.

## Tech stack

- **TUI:** `charmbracelet/bubbletea` + `bubbles` (table, textinput, viewport) + `lipgloss`.
- **LLM:** Ollama HTTP API (`/api/chat`) with `format` = JSON schema for constrained
  structured output. Thin client, no SDK.
- **DuckDB:** `marcboeker/go-duckdb` (cgo — needs a C toolchain). Kept **behind the
  `Store` interface** so a pure-Go SQLite fallback stays possible.
- **HTML:** `PuerkitoBio/goquery` (parse) + `go-shiori/go-readability` (main content).
- **Search:** Tavily REST via `net/http`.
- **Config:** TOML at `~/.config/fetch/config.toml` (role→model map, data dir); API
  keys via env (`TAVILY_API_KEY`).
- **Module layout:** `cmd/fetch` + `internal/{tui,agent,pipeline,engine,
  providers/{search,fetch,store,artifacts},config}`.

## v1 scope

**In:** four screens; converge-to-reviewable-draft interview; deterministic engine for
all five step types (**HTTP fetch only**); Tavily search; role-based Ollama models;
agent fallback with bounded retries + self-healing candidate revisions
(promote/auto-promote); DuckDB results (behind `Store`) + on-disk artifacts; run history.

**Out (later layers):** scheduling/daemon; headless browser (interface + stub only);
multiple search providers; exports; multi-user/auth.

## Testing (TDD throughout)

- `LLM`, `Search`, `Fetcher`, `Store` are interfaces → core logic (templating, plan
  validation, engine sequencing, fallback orchestration, self-heal promotion) unit-tested
  with **fakes returning canned responses** — fully deterministic.
- Golden-file tests for prompt rendering and schema→DuckDB-DDL generation.
- Opt-in integration suite behind a build tag hitting **real Ollama + Tavily**
  (`go test -tags=integration`).

## Open prerequisites

- Go toolchain not yet installed on PATH (install before implementation).
- Xcode command-line tools / C compiler for cgo (go-duckdb).
- `TAVILY_API_KEY` to be obtained.
