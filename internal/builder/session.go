package builder

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/cole/fetch/internal/agent"
	"github.com/cole/fetch/internal/config"
	"github.com/cole/fetch/internal/core"
	"github.com/cole/fetch/internal/pipeline"
	"github.com/cole/fetch/internal/providers/store"
)

// SessionDeps are the CreateSession's collaborators and tunables.
type SessionDeps struct {
	LLM        agent.LLM
	Cfg        config.Config
	Store      store.Store
	Repo       *pipeline.Repository
	Now        func() time.Time
	IDGen      func() string
	MaxRepairs int
}

// CreateSession orchestrates interview -> design -> plan(repair) -> accept.
type CreateSession struct {
	deps      SessionDeps
	iv        *Interviewer
	designer  *SchemaDesigner
	planner   *Planner
	state     InterviewState
	lastDraft Draft
}

// NewSession builds a CreateSession, filling defaults.
func NewSession(d SessionDeps) *CreateSession {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.IDGen == nil {
		d.IDGen = defaultIDGen()
	}
	if d.MaxRepairs == 0 {
		d.MaxRepairs = 2
	}
	return &CreateSession{
		deps:     d,
		iv:       NewInterviewer(d.LLM, d.Cfg),
		designer: NewSchemaDesigner(d.LLM, d.Cfg),
		planner:  NewPlanner(d.LLM, d.Cfg),
	}
}

func defaultIDGen() func() string {
	var mu sync.Mutex
	var n int
	return func() string {
		mu.Lock()
		n++
		id := n
		mu.Unlock()
		return fmt.Sprintf("%d-%d", time.Now().Unix(), id)
	}
}

// Start sets the user's opening goal.
func (s *CreateSession) Start(goal string) {
	s.state = InterviewState{Goal: goal}
}

// Reply feeds an optional user message and runs one interview turn.
func (s *CreateSession) Reply(ctx context.Context, msg string) (string, bool, error) {
	if msg != "" {
		s.state.Transcript = append(s.state.Transcript, Turn{Role: "user", Content: msg})
	}
	q, ready, facts, err := s.iv.Next(ctx, s.state)
	if err != nil {
		return "", false, err
	}
	s.state.Facts = facts
	if q != "" {
		s.state.Transcript = append(s.state.Transcript, Turn{Role: "assistant", Content: q})
	}
	s.state.Done = ready
	return q, ready, nil
}

// Finalize produces the first draft from the gathered facts.
func (s *CreateSession) Finalize(ctx context.Context) (Draft, error) {
	return s.design(ctx, "")
}

// Redraft revises the last draft per a natural-language comment.
func (s *CreateSession) Redraft(ctx context.Context, comment string) (Draft, error) {
	prior, _ := json.Marshal(s.lastDraft.Pipeline)
	fb := fmt.Sprintf("Revise the previous draft per this feedback: %s\nPrevious draft: %s", comment, prior)
	return s.design(ctx, fb)
}

func (s *CreateSession) design(ctx context.Context, feedback string) (Draft, error) {
	out, err := s.designer.Design(ctx, s.state.Goal, s.state.Facts, feedback)
	if err != nil {
		return Draft{}, err
	}
	base := core.Pipeline{
		ID:          "draft", // temporary, satisfies pipeline.Validate; replaced on Accept
		Name:        out.Name,
		Description: out.Description,
		Domain:      out.Domain,
		Inputs:      out.Inputs,
		Schema:      out.Schema,
	}
	full, err := s.planner.PlanWithRepair(ctx, base, s.state.Facts, feedback, s.deps.MaxRepairs)
	if err != nil {
		return Draft{}, err
	}
	d := Draft{Pipeline: full}
	s.lastDraft = d
	return d, nil
}

// Accept assigns an id, ensures the results table, and saves the pipeline JSON.
func (s *CreateSession) Accept(ctx context.Context, d Draft) (string, error) {
	p := d.Pipeline
	p.ID = s.assignID(p.Name)
	p.Version = 1
	p.CreatedAt = s.deps.Now()
	if err := s.deps.Store.EnsureTable(ctx, p.ID, p.Schema); err != nil {
		return "", fmt.Errorf("ensure table: %w", err)
	}
	if err := s.deps.Repo.Save(p); err != nil {
		return "", fmt.Errorf("save pipeline: %w", err)
	}
	return p.ID, nil
}

func (s *CreateSession) assignID(name string) string {
	id := slugify(name)
	if _, err := s.deps.Repo.Load(id); err == nil {
		id = id + "-" + s.deps.IDGen()
	}
	return id
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(name string) string {
	s := nonSlug.ReplaceAllString(strings.ToLower(name), "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "pipeline"
	}
	return s
}
