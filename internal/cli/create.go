package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/cole/fetch/internal/agent"
	"github.com/cole/fetch/internal/builder"
	"github.com/cole/fetch/internal/config"
	"github.com/cole/fetch/internal/pipeline"
	"github.com/cole/fetch/internal/providers/store"
)

// creator is the slice of CreateSession the terminal loop drives (so the loop
// is testable with a fake).
type creator interface {
	Start(goal string)
	Reply(ctx context.Context, msg string) (string, bool, error)
	Finalize(ctx context.Context) (builder.Draft, error)
	Redraft(ctx context.Context, comment string) (builder.Draft, error)
	Accept(ctx context.Context, d builder.Draft) (string, error)
}

func createPipeline(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	cfg, err := config.Load(defaultConfigPath())
	if err != nil {
		fmt.Fprintf(stderr, "warning: config: %v (using defaults)\n", err)
	}
	rowStore, err := store.OpenDuckDB(defaultDBPath(cfg))
	if err != nil {
		fmt.Fprintf(stderr, "error: open store: %v\n", err)
		return 1
	}
	defer rowStore.Close()

	sess := builder.NewSession(builder.SessionDeps{
		LLM:   agent.NewOllama(cfg.Ollama.BaseURL, http.DefaultClient),
		Cfg:   cfg,
		Store: rowStore,
		Repo:  pipeline.NewRepository(cfg.DataDir),
	})
	return createLoop(context.Background(), sess, stdin, stdout, stderr)
}

// createLoop runs the interactive interview -> draft -> accept/comment/cancel loop.
func createLoop(ctx context.Context, c creator, in io.Reader, out, errw io.Writer) int {
	sc := bufio.NewScanner(in)
	fmt.Fprintln(out, "Describe the pipeline you want to build (/cancel to abort):")
	if !sc.Scan() {
		return 1
	}
	goal := strings.TrimSpace(sc.Text())
	if goal == "" || goal == "/cancel" {
		fmt.Fprintln(out, "cancelled")
		return 1
	}
	c.Start(goal)

	q, ready, err := c.Reply(ctx, "")
	for err == nil && !ready {
		if q != "" {
			fmt.Fprintf(out, "\n%s\n> ", q)
		}
		if !sc.Scan() {
			break
		}
		line := strings.TrimSpace(sc.Text())
		if line == "/cancel" {
			fmt.Fprintln(out, "cancelled")
			return 1
		}
		if line == "/done" {
			break
		}
		q, ready, err = c.Reply(ctx, line)
	}
	if err != nil {
		fmt.Fprintf(errw, "interview error: %v\n", err)
		return 1
	}

	draft, err := c.Finalize(ctx)
	if err != nil {
		fmt.Fprintf(errw, "draft error: %v\n", err)
		return 1
	}
	for {
		fmt.Fprint(out, renderDraft(draft))
		fmt.Fprint(out, "\n[accept] save · [comment <text>] revise · [cancel] abort\n> ")
		if !sc.Scan() {
			return 1
		}
		action, arg := parseAction(strings.TrimSpace(sc.Text()))
		switch action {
		case "accept":
			id, err := c.Accept(ctx, draft)
			if err != nil {
				fmt.Fprintf(errw, "save error: %v\n", err)
				return 1
			}
			fmt.Fprintf(out, "saved pipeline %q\n", id)
			return 0
		case "comment":
			draft, err = c.Redraft(ctx, arg)
			if err != nil {
				fmt.Fprintf(errw, "redraft error: %v\n", err)
				return 1
			}
		case "cancel":
			fmt.Fprintln(out, "cancelled")
			return 1
		default:
			fmt.Fprintln(out, "unknown action; type accept, comment <text>, or cancel")
		}
	}
}

func parseAction(line string) (string, string) {
	parts := strings.SplitN(line, " ", 2)
	if len(parts) == 2 {
		return parts[0], strings.TrimSpace(parts[1])
	}
	return parts[0], ""
}

func renderDraft(d builder.Draft) string {
	p := d.Pipeline
	var b strings.Builder
	fmt.Fprintf(&b, "\n=== Draft: %s ===\n", p.Name)
	if p.Description != "" {
		fmt.Fprintf(&b, "%s\n", p.Description)
	}
	b.WriteString("Inputs:\n")
	for _, in := range p.Inputs {
		fmt.Fprintf(&b, "  - %s (%s)\n", in.Name, in.Type)
	}
	b.WriteString("Schema:\n")
	for _, f := range p.Schema {
		fmt.Fprintf(&b, "  - %s (%s)\n", f.Name, f.Type)
	}
	b.WriteString("Plan:\n")
	for _, s := range p.Plan {
		fmt.Fprintf(&b, "  - %s [%s] deps=%v\n", s.ID, s.Type, s.DependsOn)
	}
	return b.String()
}
