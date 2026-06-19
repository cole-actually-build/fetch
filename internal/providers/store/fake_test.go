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
