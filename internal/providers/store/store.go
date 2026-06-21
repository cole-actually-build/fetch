// Package store persists pipeline output rows and run/trace records in DuckDB.
package store

import (
	"context"

	"github.com/cole/fetch/internal/core"
)

// Store is fetch's structured-result and run-history persistence.
type Store interface {
	EnsureTable(ctx context.Context, pipelineID string, fields []core.Field) error
	AppendRows(ctx context.Context, pipelineID string, fields []core.Field, runID string, rows []map[string]any) error
	Query(ctx context.Context, query string) ([]map[string]any, error)
	RecordRun(ctx context.Context, r core.Run) error
	RecordTrace(ctx context.Context, t core.StepTrace) error
	ListRuns(ctx context.Context, pipelineID string) ([]core.Run, error)
	ResultRows(ctx context.Context, pipelineID string, runID string) ([]map[string]any, error)
	RunTraces(ctx context.Context, runID string) ([]core.StepTrace, error)
	Close() error
}
