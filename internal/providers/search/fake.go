package search

import "context"

var _ Search = (*FakeSearch)(nil)

// FakeSearch returns canned results and records queries, for tests.
type FakeSearch struct {
	Results []Result
	Err     error
	Queries []string
}

func (f *FakeSearch) Search(_ context.Context, query string, _ Options) ([]Result, error) {
	f.Queries = append(f.Queries, query)
	if f.Err != nil {
		return nil, f.Err
	}
	return f.Results, nil
}
