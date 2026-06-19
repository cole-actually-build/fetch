package pipeline

import (
	"reflect"
	"testing"
)

func TestResolveWholeValuePreservesType(t *testing.T) {
	sc := Scope{
		Input: map[string]any{"part": "12345"},
		Steps: map[string]map[string]any{
			"search": {"urls": []string{"https://a", "https://b"}},
		},
	}
	out, err := Resolve(map[string]any{
		"query": "{{input.part}} cross reference",
		"urls":  "{{steps.search.urls}}",
	}, sc)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if out["query"] != "12345 cross reference" {
		t.Fatalf("query = %v", out["query"])
	}
	urls, ok := out["urls"].([]string)
	if !ok || !reflect.DeepEqual(urls, []string{"https://a", "https://b"}) {
		t.Fatalf("urls not preserved as []string: %#v", out["urls"])
	}
}

func TestResolveNested(t *testing.T) {
	sc := Scope{Input: map[string]any{"n": 5}}
	out, err := Resolve(map[string]any{
		"opts": map[string]any{"max": "{{input.n}}"},
		"list": []any{"{{input.n}}", "static"},
	}, sc)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	opts := out["opts"].(map[string]any)
	if opts["max"] != 5 { // whole-value, type preserved (int)
		t.Fatalf("nested whole-value = %#v", opts["max"])
	}
	list := out["list"].([]any)
	if list[0] != 5 || list[1] != "static" {
		t.Fatalf("list = %#v", list)
	}
}

func TestResolveUnknownReference(t *testing.T) {
	if _, err := Resolve(map[string]any{"x": "{{steps.nope.field}}"}, Scope{}); err == nil {
		t.Fatal("expected error for unknown reference")
	}
}
