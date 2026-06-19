package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

var _ Search = (*Tavily)(nil)

// Tavily implements Search via the Tavily REST API.
type Tavily struct {
	baseURL string
	apiKey  string
	hc      *http.Client
}

func NewTavily(baseURL, apiKey string, hc *http.Client) *Tavily {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Tavily{baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, hc: hc}
}

type tavilyReq struct {
	APIKey            string `json:"api_key"`
	Query             string `json:"query"`
	MaxResults        int    `json:"max_results"`
	IncludeRawContent bool   `json:"include_raw_content"`
}

type tavilyResp struct {
	Results []struct {
		Title      string  `json:"title"`
		URL        string  `json:"url"`
		Content    string  `json:"content"`
		RawContent string  `json:"raw_content"`
		Score      float64 `json:"score"`
	} `json:"results"`
}

func (t *Tavily) Search(ctx context.Context, query string, opts Options) ([]Result, error) {
	max := opts.MaxResults
	if max <= 0 {
		max = 5
	}
	buf, err := json.Marshal(tavilyReq{
		APIKey:            t.apiKey,
		Query:             query,
		MaxResults:        max,
		IncludeRawContent: true,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL+"/search", bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tavily search: status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var parsed tavilyResp
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("tavily search: decode: %w", err)
	}
	out := make([]Result, 0, len(parsed.Results))
	for _, r := range parsed.Results {
		content := r.RawContent
		if content == "" {
			content = r.Content
		}
		out = append(out, Result{
			Title:   r.Title,
			URL:     r.URL,
			Snippet: r.Content,
			Content: content,
			Score:   r.Score,
		})
	}
	return out, nil
}
