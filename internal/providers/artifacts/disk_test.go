package artifacts

import (
	"bytes"
	"context"
	"testing"
)

func TestDiskPutGetRoundTrip(t *testing.T) {
	d := NewDisk(t.TempDir())
	data := []byte("<html>raw</html>")
	ref, err := d.Put(context.Background(), "run1", "stepA", data, "html")
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if ref == "" {
		t.Fatal("empty ref")
	}
	got, err := d.Get(context.Background(), ref)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("round trip mismatch: %q", got)
	}
}

func TestDiskPutIsContentAddressed(t *testing.T) {
	d := NewDisk(t.TempDir())
	r1, _ := d.Put(context.Background(), "run1", "stepA", []byte("same"), "json")
	r2, _ := d.Put(context.Background(), "run1", "stepA", []byte("same"), "json")
	if r1 != r2 {
		t.Fatalf("identical content should yield identical ref: %q vs %q", r1, r2)
	}
	r3, _ := d.Put(context.Background(), "run1", "stepA", []byte("different"), "json")
	if r3 == r1 {
		t.Fatal("different content should yield different ref")
	}
}

func TestDiskGetMissing(t *testing.T) {
	d := NewDisk(t.TempDir())
	if _, err := d.Get(context.Background(), "run1/stepA/deadbeef.json"); err == nil {
		t.Fatal("expected error for missing ref")
	}
}
