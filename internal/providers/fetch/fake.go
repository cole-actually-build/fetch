package fetch

import "context"

var _ Fetcher = (*FakeFetcher)(nil)

// FakeFetcher serves canned pages keyed by URL, for tests.
type FakeFetcher struct {
	Pages map[string]Page
	Err   error
	URLs  []string
}

func (f *FakeFetcher) Methods() []Method { return []Method{MethodHTTP} }

func (f *FakeFetcher) Fetch(_ context.Context, url string, _ Method) (Page, error) {
	f.URLs = append(f.URLs, url)
	if f.Err != nil {
		return Page{}, f.Err
	}
	if p, ok := f.Pages[url]; ok {
		return p, nil
	}
	return Page{URL: url, StatusCode: 404}, nil
}
