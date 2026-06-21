package store

import (
	"context"
	"os"
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

func TestDuckDBReadMethods(t *testing.T) {
	d, err := OpenDuckDB(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx := context.Background()

	fields := []core.Field{{Name: "title", Type: core.FieldString}, {Name: "rank", Type: core.FieldInt}}
	if err := d.EnsureTable(ctx, "p1", fields); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	if err := d.RecordRun(ctx, core.Run{ID: "r1", PipelineID: "p1", Input: map[string]any{"q": "x"}, Status: core.RunOK, StartedAt: now, FinishedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := d.AppendRows(ctx, "p1", fields, "r1", []map[string]any{{"title": "A", "rank": 1}, {"title": "B", "rank": 2}}); err != nil {
		t.Fatal(err)
	}
	if err := d.RecordTrace(ctx, core.StepTrace{RunID: "r1", StepID: "s1", Status: "ok", OutputSummary: "2 rows", ArtifactRefs: []string{"a/b"}, Tokens: 7, FallbackUsed: true}); err != nil {
		t.Fatal(err)
	}

	runs, err := d.ListRuns(ctx, "p1")
	if err != nil || len(runs) != 1 || runs[0].ID != "r1" || runs[0].Status != core.RunOK || runs[0].Input["q"] != "x" {
		t.Fatalf("ListRuns = %+v err=%v", runs, err)
	}
	rows, err := d.ResultRows(ctx, "p1", "r1")
	if err != nil || len(rows) != 2 {
		t.Fatalf("ResultRows len=%d err=%v", len(rows), err)
	}
	traces, err := d.RunTraces(ctx, "r1")
	if err != nil || len(traces) != 1 || traces[0].StepID != "s1" || !traces[0].FallbackUsed || traces[0].Tokens != 7 || len(traces[0].ArtifactRefs) != 1 {
		t.Fatalf("RunTraces = %+v err=%v", traces, err)
	}
}

func TestOpenDuckDBCreatesParentDir(t *testing.T) {
	// nested path whose parent dirs do not exist yet
	path := filepath.Join(t.TempDir(), "a", "b", "fetch.duckdb")
	d, err := OpenDuckDB(path)
	if err != nil {
		t.Fatalf("OpenDuckDB should create the parent dir: %v", err)
	}
	defer d.Close()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("db file not created: %v", err)
	}
}
