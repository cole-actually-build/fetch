package agent

import "context"

var _ LLM = (*FakeLLM)(nil)

// FakeLLM is a deterministic LLM for tests in this and later plans. It returns
// queued Responses in order; once exhausted it repeats the last one.
type FakeLLM struct {
	Responses []ChatResponse
	Err       error
	Calls     []ChatRequest
}

func (f *FakeLLM) Chat(_ context.Context, req ChatRequest) (ChatResponse, error) {
	f.Calls = append(f.Calls, req)
	if f.Err != nil {
		return ChatResponse{}, f.Err
	}
	if len(f.Responses) == 0 {
		return ChatResponse{}, nil
	}
	idx := len(f.Calls) - 1
	if idx >= len(f.Responses) {
		idx = len(f.Responses) - 1
	}
	return f.Responses[idx], nil
}
