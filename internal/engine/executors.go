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
