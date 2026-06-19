// Package fetch defines the page-fetch capability. v1 ships an HTTP backend;
// a headless-browser backend is a later layer behind the same interface.
package fetch

import (
	"context"
	"errors"
)

// Method selects how a page is retrieved.
type Method string

const (
	MethodHTTP    Method = "http"
	MethodBrowser Method = "browser"
)

// ErrMethodUnavailable is returned when a Fetcher is asked for a method it
// does not implement (e.g. browser in v1).
var ErrMethodUnavailable = errors.New("fetch: method unavailable")

// Page is a fetched document. Raw is the original bytes (stored as an
// artifact); Text is cleaned main-content for extraction.
type Page struct {
	URL         string
	StatusCode  int
	ContentType string
	Raw         []byte
	Text        string
}

// Fetcher retrieves a single URL using the requested method.
type Fetcher interface {
	Fetch(ctx context.Context, url string, method Method) (Page, error)
	Methods() []Method
}
