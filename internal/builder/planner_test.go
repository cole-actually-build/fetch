package builder

import (
	"context"
	"testing"

	"github.com/cole/fetch/internal/agent"
	"github.com/cole/fetch/internal/config"
	"github.com/cole/fetch/internal/core"
)

func base() core.Pipeline {
	return core.Pipeline{
		ID:     "p",
		Name:   "P",
		Inputs: []core.InputParam{{Name: "q", Type: core.FieldString}},
		Schema: []core.Field{{Name: "title", Type: core.FieldString}},
	}
}

const validPlanJSON = `{"plan":[
 {"id":"search","name":"search","type":"search","params":{"query":"{{input.q}}"},"depends_on":[]},
 {"id":"store","name":"store","type":"store","params":{"rows":"{{steps.search.results}}"},"depends_on":["search"]}
]}`

// references an unknown dep -> pipeline.Validate fails -> triggers a repair.
const invalidPlanJSON = `{"plan":[
 {"id":"store","name":"store","type":"store","params":{},"depends_on":["ghost"]}
]}`

func TestPlannerSingleCall(t *testing.T) {
	llm := &agent.FakeLLM{Responses: []agent.ChatResponse{{Content: validPlanJSON}}}
	p := NewPlanner(llm, config.Default())
	steps, err := p.Plan(context.Background(), base(), Facts{}, "")
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(steps) != 2 || steps[0].ID != "search" || steps[1].Type != core.StepStore {
		t.Fatalf("steps = %+v", steps)
	}
	if llm.Calls[len(llm.Calls)-1].Model != config.Default().ModelFor(config.RolePlan) {
		t.Fatalf("model = %q", llm.Calls[0].Model)
	}
}

func TestPlanWithRepairFixesInvalidPlan(t *testing.T) {
	llm := &agent.FakeLLM{Responses: []agent.ChatResponse{
		{Content: invalidPlanJSON}, // first attempt: invalid
		{Content: validPlanJSON},   // repair: valid
	}}
	p := NewPlanner(llm, config.Default())
	full, err := p.PlanWithRepair(context.Background(), base(), Facts{}, "", 2)
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if len(full.Plan) != 2 {
		t.Fatalf("plan = %+v", full.Plan)
	}
	if len(llm.Calls) != 2 {
		t.Fatalf("expected 2 calls (initial + 1 repair), got %d", len(llm.Calls))
	}
}

func TestPlanWithRepairGivesUp(t *testing.T) {
	llm := &agent.FakeLLM{Responses: []agent.ChatResponse{{Content: invalidPlanJSON}}}
	p := NewPlanner(llm, config.Default())
	if _, err := p.PlanWithRepair(context.Background(), base(), Facts{}, "", 1); err == nil {
		t.Fatal("expected error after exhausting repairs")
	}
	// 1 initial + 1 repair = 2 attempts.
	if len(llm.Calls) != 2 {
		t.Fatalf("calls = %d", len(llm.Calls))
	}
}
