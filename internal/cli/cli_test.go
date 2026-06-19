package cli

import (
	"bytes"
	"testing"
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
	code := Run(nil, &out, &errBuf)
	if code == 0 {
		t.Fatal("expected non-zero exit for no args")
	}
	if errBuf.Len() == 0 {
		t.Fatal("expected usage on stderr")
	}
}

func TestRunUnknownCommand(t *testing.T) {
	var out, errBuf bytes.Buffer
	if code := Run([]string{"frobnicate"}, &out, &errBuf); code == 0 {
		t.Fatal("expected non-zero exit for unknown command")
	}
}
