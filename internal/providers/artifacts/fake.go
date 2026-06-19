package artifacts

import (
	"context"
	"fmt"
	"sync"
)

var _ Store = (*FakeArtifacts)(nil)

// FakeArtifacts is an in-memory artifact Store for engine tests.
type FakeArtifacts struct {
	mu   sync.Mutex
	data map[string][]byte
	seq  int
}

func NewFakeArtifacts() *FakeArtifacts {
	return &FakeArtifacts{data: map[string][]byte{}}
}

func (f *FakeArtifacts) Put(_ context.Context, runID, stepID string, data []byte, ext string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ref := fmt.Sprintf("%s/%s/%d.%s", runID, stepID, f.seq, ext)
	f.seq++
	cp := make([]byte, len(data))
	copy(cp, data)
	f.data[ref] = cp
	return ref, nil
}

func (f *FakeArtifacts) Get(_ context.Context, ref string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.data[ref]
	if !ok {
		return nil, fmt.Errorf("artifact not found: %s", ref)
	}
	return b, nil
}
