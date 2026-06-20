package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/cole/fetch/internal/builder"
	"github.com/cole/fetch/internal/core"
)

func TestParseInputs(t *testing.T) {
	m, err := parseInputs([]string{"part=A1", "year=2024", "note=has=equals"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if m["part"] != "A1" || m["year"] != "2024" || m["note"] != "has=equals" {
		t.Fatalf("parsed = %#v", m)
	}
	if _, err := parseInputs([]string{"bad"}); err == nil {
		t.Fatal("expected error for missing '='")
	}
}

func TestRunUsageWhenNoArgs(t *testing.T) {
	var out, errBuf bytes.Buffer
	if code := Run(nil, strings.NewReader(""), &out, &errBuf); code == 0 {
		t.Fatal("expected non-zero exit for no args")
	}
	if errBuf.Len() == 0 {
		t.Fatal("expected usage on stderr")
	}
}

func TestRunUnknownCommand(t *testing.T) {
	var out, errBuf bytes.Buffer
	if code := Run([]string{"frobnicate"}, strings.NewReader(""), &out, &errBuf); code == 0 {
		t.Fatal("expected non-zero exit for unknown command")
	}
}

func TestParseAction(t *testing.T) {
	a, arg := parseAction("comment rename to Foo")
	if a != "comment" || arg != "rename to Foo" {
		t.Fatalf("got %q %q", a, arg)
	}
	if a, arg := parseAction("accept"); a != "accept" || arg != "" {
		t.Fatalf("got %q %q", a, arg)
	}
}

func TestRenderDraftShowsSchemaAndPlan(t *testing.T) {
	d := builder.Draft{Pipeline: core.Pipeline{
		Name:   "P",
		Schema: []core.Field{{Name: "title", Type: core.FieldString}},
		Plan:   []core.Step{{ID: "search", Type: core.StepSearch}},
	}}
	s := renderDraft(d)
	if !strings.Contains(s, "title") || !strings.Contains(s, "search") {
		t.Fatalf("render missing fields:\n%s", s)
	}
}

// fakeCreator scripts a one-question interview then an accept.
type fakeCreator struct {
	replied  bool
	accepted bool
}

func (f *fakeCreator) Start(string) {}
func (f *fakeCreator) Reply(context.Context, string) (string, bool, error) {
	return "", true, nil // immediately ready
}
func (f *fakeCreator) Finalize(context.Context) (builder.Draft, error) {
	return builder.Draft{Pipeline: core.Pipeline{Name: "P", Schema: []core.Field{{Name: "title"}}}}, nil
}
func (f *fakeCreator) Redraft(context.Context, string) (builder.Draft, error) {
	return builder.Draft{}, nil
}
func (f *fakeCreator) Accept(context.Context, builder.Draft) (string, error) {
	f.accepted = true
	return "p", nil
}

func TestCreateLoopAcceptFlow(t *testing.T) {
	var out, errBuf bytes.Buffer
	in := strings.NewReader("build a thing\naccept\n")
	fc := &fakeCreator{}
	code := createLoop(context.Background(), fc, in, &out, &errBuf)
	if code != 0 {
		t.Fatalf("exit = %d; stderr=%s", code, errBuf.String())
	}
	if !fc.accepted {
		t.Fatal("expected Accept to be called")
	}
	if !strings.Contains(out.String(), "saved pipeline") {
		t.Fatalf("missing save confirmation:\n%s", out.String())
	}
}

func TestCreateLoopCancel(t *testing.T) {
	var out, errBuf bytes.Buffer
	in := strings.NewReader("/cancel\n")
	if code := createLoop(context.Background(), &fakeCreator{}, in, &out, &errBuf); code == 0 {
		t.Fatal("expected non-zero exit on cancel")
	}
}
