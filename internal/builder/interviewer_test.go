package builder

import (
	"context"
	"strings"
	"testing"

	"github.com/cole/fetch/internal/agent"
	"github.com/cole/fetch/internal/config"
)

func TestInterviewerAsksThenReady(t *testing.T) {
	llm := &agent.FakeLLM{Responses: []agent.ChatResponse{
		{Content: `{"question":"What part number format do you use?","ready":false,"facts":{"domain":"truck-parts","inputs":[],"output_fields":[],"source_hints":[]}}`},
		{Content: `{"question":"","ready":true,"facts":{"domain":"truck-parts","inputs":[{"name":"part","type":"string","required":true,"description":"part number"}],"output_fields":[{"name":"cross_ref","type":"string","description":"cross reference"}],"source_hints":["rockauto.com"]}}`},
	}}
	iv := NewInterviewer(llm, config.Default())

	q, ready, facts, err := iv.Next(context.Background(), InterviewState{Goal: "truck part cross references"})
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if ready || q == "" {
		t.Fatalf("first turn should ask a question: q=%q ready=%v", q, ready)
	}
	if facts.Domain != "truck-parts" {
		t.Fatalf("facts.Domain = %q", facts.Domain)
	}

	q2, ready2, facts2, err := iv.Next(context.Background(), InterviewState{
		Goal:       "truck part cross references",
		Transcript: []Turn{{Role: "assistant", Content: q}, {Role: "user", Content: "ABC-123 style"}},
	})
	if err != nil {
		t.Fatalf("next2: %v", err)
	}
	if !ready2 || q2 != "" {
		t.Fatalf("second turn should be ready: q=%q ready=%v", q2, ready2)
	}
	if len(facts2.Inputs) != 1 || facts2.Inputs[0].Name != "part" || len(facts2.OutputFields) != 1 {
		t.Fatalf("facts2 not parsed: %+v", facts2)
	}
	// the call used structured output (Format) and the interview model.
	last := llm.Calls[len(llm.Calls)-1]
	if last.Format == nil {
		t.Fatal("expected Format (JSON schema) on the interview call")
	}
	if last.Model != config.Default().ModelFor(config.RoleInterview) {
		t.Fatalf("model = %q", last.Model)
	}
}

func TestInterviewSystemPromptOneAtATime(t *testing.T) {
	sys := interviewSystemPrompt()
	if !strings.Contains(sys, "ONE") {
		t.Fatalf("system prompt must instruct one question at a time:\n%s", sys)
	}
}
