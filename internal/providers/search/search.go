// Package search defines the discovery capability and its Tavily backend.
package search

import "context"

// Result is one search hit. Content holds the fullest text available
// (provider raw content when present, else the snippet).
type Result struct {
	Title   string
	URL     string
	Snippet string
	Content string
	Score   float64
}

// Options tune a search call.
type Options struct {
	MaxResults int
}

// Search discovers candidate URLs for a query.
type Search interface {
	Search(ctx context.Context, query string, opts Options) ([]Result, error)
}
