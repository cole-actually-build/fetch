package builder

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cole/fetch/internal/agent"
	"github.com/cole/fetch/internal/config"
)

// Interviewer drives the one-question-at-a-time interview via the LLM.
type Interviewer struct {
	llm agent.LLM
	cfg config.Config
}

// NewInterviewer builds an Interviewer.
func NewInterviewer(llm agent.LLM, cfg config.Config) *Interviewer {
	return &Interviewer{llm: llm, cfg: cfg}
}

type interviewReply struct {
	Question string `json:"question"`
	Ready    bool   `json:"ready"`
	Facts    Facts  `json:"facts"`
}

// Next runs one interview turn: it returns the next question (empty when ready),
// whether the interview is complete, and the model's current understanding.
func (iv *Interviewer) Next(ctx context.Context, st InterviewState) (string, bool, Facts, error) {
	resp, err := iv.llm.Chat(ctx, agent.ChatRequest{
		Model: iv.cfg.ModelFor(config.RoleInterview),
		Messages: []agent.Message{
			{Role: "system", Content: interviewSystemPrompt()},
			{Role: "user", Content: interviewUserPrompt(st)},
		},
		Format: interviewReplySchema(),
	})
	if err != nil {
		return "", false, Facts{}, err
	}
	var r interviewReply
	if err := json.Unmarshal([]byte(resp.Content), &r); err != nil {
		return "", false, Facts{}, fmt.Errorf("interviewer: decode reply: %w", err)
	}
	return r.Question, r.Ready, r.Facts, nil
}
