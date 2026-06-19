package fetch

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPFetchReadabilityText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, `<html><head><title>Part 123</title></head><body>
			<nav>menu noise</nav>
			<article><h1>Cross Reference</h1><p>The cross reference for part 123 is XYZ-999.</p></article>
		</body></html>`)
	}))
	defer srv.Close()

	f := NewHTTP("test-agent", 10, 1<<20)
	page, err := f.Fetch(context.Background(), srv.URL, MethodHTTP)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if page.StatusCode != 200 {
		t.Fatalf("status: %d", page.StatusCode)
	}
	if len(page.Raw) == 0 {
		t.Fatal("expected raw bytes")
	}
	if !strings.Contains(page.Text, "XYZ-999") {
		t.Fatalf("readability text missing content: %q", page.Text)
	}
	if strings.Contains(page.Text, "menu noise") {
		t.Fatalf("expected nav noise stripped, got: %q", page.Text)
	}
}

func TestHTTPFetchBrowserUnavailable(t *testing.T) {
	f := NewHTTP("test-agent", 10, 1<<20)
	_, err := f.Fetch(context.Background(), "http://example.com", MethodBrowser)
	if !errors.Is(err, ErrMethodUnavailable) {
		t.Fatalf("expected ErrMethodUnavailable, got %v", err)
	}
}

func TestHTTPFetchMethods(t *testing.T) {
	f := NewHTTP("ua", 10, 1<<20)
	ms := f.Methods()
	if len(ms) != 1 || ms[0] != MethodHTTP {
		t.Fatalf("methods: %v", ms)
	}
}
