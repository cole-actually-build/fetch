package artifacts

import (
	"bytes"
	"context"
	"testing"
)

func TestFakeArtifactsImplementsStore(t *testing.T) {
	var _ Store = NewFakeArtifacts()
}

func TestFakeArtifactsRoundTrip(t *testing.T) {
	ctx := context.Background()
	fa := NewFakeArtifacts()
	ref, err := fa.Put(ctx, "run1", "s1", []byte("hello"), "txt")
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err := fa.Get(ctx, ref)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !bytes.Equal(got, []byte("hello")) {
		t.Fatalf("got %q", got)
	}
	if _, err := fa.Get(ctx, "missing"); err == nil {
		t.Fatal("expected error for missing ref")
	}
}
