package pipeline

import (
	"testing"

	"github.com/cole/fetch/internal/core"
)

func pipe(steps ...core.Step) core.Pipeline {
	return core.Pipeline{ID: "p1", Plan: steps}
}

func TestTopoOrderLinear(t *testing.T) {
	p := pipe(
		core.Step{ID: "c", Type: core.StepStore, DependsOn: []string{"b"}},
		core.Step{ID: "a", Type: core.StepSearch},
		core.Step{ID: "b", Type: core.StepFetch, DependsOn: []string{"a"}},
	)
	order, err := TopoOrder(p)
	if err != nil {
		t.Fatalf("topo: %v", err)
	}
	got := []string{order[0].ID, order[1].ID, order[2].ID}
	want := []string{"a", "b", "c"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

func TestTopoOrderCycle(t *testing.T) {
	p := pipe(
		core.Step{ID: "a", Type: core.StepSearch, DependsOn: []string{"b"}},
		core.Step{ID: "b", Type: core.StepFetch, DependsOn: []string{"a"}},
	)
	if _, err := TopoOrder(p); err == nil {
		t.Fatal("expected cycle error")
	}
}

func TestTopoOrderUnknownDep(t *testing.T) {
	p := pipe(core.Step{ID: "a", Type: core.StepSearch, DependsOn: []string{"ghost"}})
	if _, err := TopoOrder(p); err == nil {
		t.Fatal("expected unknown-dep error")
	}
}

func TestValidate(t *testing.T) {
	good := pipe(core.Step{ID: "a", Type: core.StepSearch})
	if err := Validate(good); err != nil {
		t.Fatalf("good pipeline rejected: %v", err)
	}
	if err := Validate(core.Pipeline{ID: "x"}); err == nil {
		t.Fatal("empty plan should be invalid")
	}
	if err := Validate(core.Pipeline{ID: "", Plan: []core.Step{{ID: "a", Type: core.StepSearch}}}); err == nil {
		t.Fatal("empty id should be invalid")
	}
	bad := pipe(core.Step{ID: "a", Type: core.StepType("nonsense")})
	if err := Validate(bad); err == nil {
		t.Fatal("invalid step type should be rejected")
	}
	dup := pipe(core.Step{ID: "a", Type: core.StepSearch}, core.Step{ID: "a", Type: core.StepFetch})
	if err := Validate(dup); err == nil {
		t.Fatal("duplicate step id should be rejected")
	}
}
