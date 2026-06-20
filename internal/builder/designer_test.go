package builder

import (
	"context"
	"testing"

	"github.com/cole/fetch/internal/agent"
	"github.com/cole/fetch/internal/config"
	"github.com/cole/fetch/internal/core"
)

func TestSchemaDesignerProducesSchema(t *testing.T) {
	llm := &agent.FakeLLM{Responses: []agent.ChatResponse{{
		Content: `{"name":"Truck Cross Ref","description":"cross references","domain":"truck-parts",
		"inputs":[{"name":"part","type":"string","required":true,"description":"part number"}],
		"schema":[{"name":"part","type":"string","description":"the part"},{"name":"cross_ref","type":"string","description":"x-ref"}]}`,
	}}}
	d := NewSchemaDesigner(llm, config.Default())
	out, err := d.Design(context.Background(), "truck cross refs", Facts{Domain: "truck-parts"}, "")
	if err != nil {
		t.Fatalf("design: %v", err)
	}
	if out.Name != "Truck Cross Ref" || out.Domain != "truck-parts" {
		t.Fatalf("out = %+v", out)
	}
	if len(out.Inputs) != 1 || out.Inputs[0].Type != core.FieldString {
		t.Fatalf("inputs = %+v", out.Inputs)
	}
	if len(out.Schema) != 2 || out.Schema[1].Name != "cross_ref" {
		t.Fatalf("schema = %+v", out.Schema)
	}
	last := llm.Calls[len(llm.Calls)-1]
	if last.Format == nil {
		t.Fatal("expected Format on schema call")
	}
	if last.Model != config.Default().ModelFor(config.RoleSchema) {
		t.Fatalf("model = %q", last.Model)
	}
}
