package pipeline

import (
	"testing"

	"github.com/cole/fetch/internal/core"
)

func TestRepositorySaveLoadListDelete(t *testing.T) {
	r := NewRepository(t.TempDir())
	p := core.Pipeline{ID: "truck-xref", Name: "Truck cross-ref", Version: 1,
		Plan: []core.Step{{ID: "s", Type: core.StepSearch}}}
	if err := r.Save(p); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := r.Load("truck-xref")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Name != "Truck cross-ref" || got.Version != 1 {
		t.Fatalf("loaded wrong: %+v", got)
	}
	list, err := r.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].ID != "truck-xref" {
		t.Fatalf("list wrong: %+v", list)
	}
	if err := r.Delete("truck-xref"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := r.Load("truck-xref"); err == nil {
		t.Fatal("expected load error after delete")
	}
}
