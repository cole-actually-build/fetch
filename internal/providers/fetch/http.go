package fetch

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	readability "github.com/go-shiori/go-readability"
)

var _ Fetcher = (*HTTPFetcher)(nil)

// HTTPFetcher retrieves pages with a plain HTTP GET.
type HTTPFetcher struct {
	userAgent string
	maxBytes  int64
	hc        *http.Client
}

func NewHTTP(userAgent string, timeoutSeconds int, maxBytes int64) *HTTPFetcher {
	if maxBytes <= 0 {
		maxBytes = 5 << 20
	}
	return &HTTPFetcher{
		userAgent: userAgent,
		maxBytes:  maxBytes,
		hc:        &http.Client{Timeout: time.Duration(timeoutSeconds) * time.Second},
	}
}

func (h *HTTPFetcher) Methods() []Method { return []Method{MethodHTTP} }

func (h *HTTPFetcher) Fetch(ctx context.Context, rawURL string, method Method) (Page, error) {
	if method != MethodHTTP {
		return Page{}, fmt.Errorf("%w: %s", ErrMethodUnavailable, method)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return Page{}, err
	}
	req.Header.Set("User-Agent", h.userAgent)

	resp, err := h.hc.Do(req)
	if err != nil {
		return Page{}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, h.maxBytes))
	if err != nil {
		return Page{}, err
	}
	ct := resp.Header.Get("Content-Type")
	page := Page{
		URL:         rawURL,
		StatusCode:  resp.StatusCode,
		ContentType: ct,
		Raw:         body,
	}
	if strings.Contains(ct, "html") {
		page.Text = extractReadable(body, rawURL)
	} else {
		page.Text = string(body)
	}
	return page, nil
}

func extractReadable(body []byte, rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return string(body)
	}
	article, err := readability.FromReader(bytes.NewReader(body), parsed)
	if err != nil || strings.TrimSpace(article.TextContent) == "" {
		return string(body)
	}
	return strings.TrimSpace(article.TextContent)
}
