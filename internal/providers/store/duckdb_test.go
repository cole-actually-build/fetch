package store

import (
	"context"
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
	ctx := context.Background()
	db := openTemp(t)
	fields := []core.Field{
		{Name: "cross_ref", Type: core.FieldString},
		{Name: "price", Type: core.FieldFloat},
		{Name: "in_stock", Type: core.FieldBool},
	}
	if err := db.EnsureTable(ctx, "pipe1", fields); err != nil {
		t.Fatalf("ensure table: %v", err)
	}
	// idempotent
	if err := db.EnsureTable(ctx, "pipe1", fields); err != nil {
		t.Fatalf("ensure table twice: %v", err)
	}
	rows := []map[string]any{
		{"cross_ref": "XYZ-1", "price": 12.5, "in_stock": true},
		{"cross_ref": "XYZ-2", "price": 9.0, "in_stock": false},
	}
	if err := db.AppendRows(ctx, "pipe1", fields, "run-1", rows); err != nil {
		t.Fatalf("append: %v", err)
	}
	got, err := db.Query(ctx, `SELECT cross_ref, price, in_stock, __run_id FROM data_pipe1 ORDER BY cross_ref`)
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
	ctx := context.Background()
	db := openTemp(t)
	run := core.Run{
		ID: "run-1", PipelineID: "pipe1",
		Input:     map[string]any{"part_number": "123"},
		Status:    core.RunOK,
		StartedAt: time.Now().UTC(), FinishedAt: time.Now().UTC(),
	}
	if err := db.RecordRun(ctx, run); err != nil {
		t.Fatalf("record run: %v", err)
	}
	tr := core.StepTrace{RunID: "run-1", StepID: "s1", Status: "ok", FallbackUsed: true, Tokens: 42}
	if err := db.RecordTrace(ctx, tr); err != nil {
		t.Fatalf("record trace: %v", err)
	}
	got, err := db.Query(ctx, `SELECT id, status FROM runs WHERE id = 'run-1'`)
	if err != nil {
		t.Fatalf("query runs: %v", err)
	}
	if len(got) != 1 || got[0]["status"] != string(core.RunOK) {
		t.Fatalf("run not recorded: %+v", got)
	}
	traces, err := db.Query(ctx, `SELECT step_id, fallback_used FROM step_traces WHERE run_id = 'run-1'`)
	if err != nil {
		t.Fatalf("query traces: %v", err)
	}
	if len(traces) != 1 {
		t.Fatalf("trace not recorded: %+v", traces)
	}
}

func TestSanitizeRejectsInjection(t *testing.T) {
	ctx := context.Background()
	db := openTemp(t)
	// A pipeline id with punctuation must not break table creation.
	if err := db.EnsureTable(ctx, "weird-id.drop", []core.Field{{Name: "x", Type: core.FieldString}}); err != nil {
		t.Fatalf("ensure table sanitized: %v", err)
	}
	if err := db.AppendRows(ctx, "weird-id.drop", []core.Field{{Name: "x", Type: core.FieldString}}, "r", []map[string]any{{"x": "ok"}}); err != nil {
		t.Fatalf("append sanitized: %v", err)
	}
}
