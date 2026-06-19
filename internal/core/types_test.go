package core

import (
	"encoding/json"
	"testing"
	"time"
)

func TestPipelineJSONRoundTrip(t *testing.T) {
	in := Pipeline{
		ID:          "p1",
		Name:        "Truck part cross-ref",
		Description: "Find cross references for a part number",
		Domain:      "truck-parts",
		Inputs:      []InputParam{{Name: "part_number", Type: FieldString, Required: true, Description: "OEM part #"}},
		Schema:      []Field{{Name: "cross_ref", Type: FieldString, Description: "cross reference number"}},
		Plan: []Step{{
			ID:        "s1",
			Name:      "search",
			Type:      StepSearch,
			Params:    map[string]any{"query": "{{input.part_number}} cross reference"},
			DependsOn: nil,
		}},
		Models:    map[string]string{"extract": "qwen3-coder:latest"},
		CreatedAt: time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC),
		Version:   1,
	}

	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out Pipeline
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.ID != in.ID || out.Version != in.Version || len(out.Plan) != 1 {
		t.Fatalf("round trip mismatch: %+v", out)
	}
	if out.Plan[0].Type != StepSearch {
		t.Fatalf("step type lost: %q", out.Plan[0].Type)
	}
	if out.Schema[0].Type != FieldString {
		t.Fatalf("field type lost: %q", out.Schema[0].Type)
	}
}
