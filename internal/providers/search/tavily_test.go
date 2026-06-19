package search

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTavilySearchParsesResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		json.Unmarshal(raw, &body)
		if body["query"] != "truck part 12345" {
			t.Errorf("query not forwarded: %v", body["query"])
		}
		if body["api_key"] != "key-abc" {
			t.Errorf("api key not forwarded: %v", body["api_key"])
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"results":[
			{"title":"Cross Ref","url":"https://ex.com/a","content":"snippet a","score":0.9,"raw_content":"full a"},
			{"title":"Catalog","url":"https://ex.com/b","content":"snippet b","score":0.5}
		]}`)
	}))
	defer srv.Close()

	s := NewTavily(srv.URL, "key-abc", srv.Client())
	got, err := s.Search(context.Background(), "truck part 12345", Options{MaxResults: 5})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 results, got %d", len(got))
	}
	if got[0].URL != "https://ex.com/a" || got[0].Score != 0.9 {
		t.Fatalf("result 0 wrong: %+v", got[0])
	}
	if got[0].Content != "full a" {
		t.Fatalf("expected raw_content preferred for Content, got %q", got[0].Content)
	}
	if got[1].Content != "snippet b" {
		t.Fatalf("expected content fallback, got %q", got[1].Content)
	}
}

func TestTavilySearchErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()
	s := NewTavily(srv.URL, "bad", srv.Client())
	if _, err := s.Search(context.Background(), "q", Options{}); err == nil {
		t.Fatal("expected error on 401")
	}
}
