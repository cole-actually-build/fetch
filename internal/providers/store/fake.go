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
