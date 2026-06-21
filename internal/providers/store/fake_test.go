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

func TestFakeStoreReadMethods(t *testing.T) {
	fs := NewFakeStore()
	ctx := context.Background()
	_ = fs.EnsureTable(ctx, "p1", []core.Field{{Name: "title", Type: core.FieldString}})
	_ = fs.RecordRun(ctx, core.Run{ID: "r1", PipelineID: "p1", Status: core.RunOK})
	_ = fs.RecordRun(ctx, core.Run{ID: "r2", PipelineID: "other", Status: core.RunOK})
	_ = fs.AppendRows(ctx, "p1", nil, "r1", []map[string]any{{"title": "A"}})
	_ = fs.RecordTrace(ctx, core.StepTrace{RunID: "r1", StepID: "s1"})

	if runs, _ := fs.ListRuns(ctx, "p1"); len(runs) != 1 || runs[0].ID != "r1" {
		t.Fatalf("ListRuns = %+v", runs)
	}
	if rows, _ := fs.ResultRows(ctx, "p1", "r1"); len(rows) != 1 || rows[0]["title"] != "A" {
		t.Fatalf("ResultRows = %+v", rows)
	}
	if tr, _ := fs.RunTraces(ctx, "r1"); len(tr) != 1 || tr[0].StepID != "s1" {
		t.Fatalf("RunTraces = %+v", tr)
	}
}
