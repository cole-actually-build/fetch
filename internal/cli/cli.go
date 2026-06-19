// Package cli is the thin command-line entry point for fetch. v1 supports
// `fetch run <pipeline.json> [--input k=v ...]`.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/cole/fetch/internal/agent"
	"github.com/cole/fetch/internal/config"
	"github.com/cole/fetch/internal/core"
	"github.com/cole/fetch/internal/engine"
	"github.com/cole/fetch/internal/pipeline"
	"github.com/cole/fetch/internal/providers/artifacts"
	"github.com/cole/fetch/internal/providers/fetch"
	"github.com/cole/fetch/internal/providers/search"
	"github.com/cole/fetch/internal/providers/store"
)

const usage = `fetch — agentic web research pipelines

Usage:
  fetch run <pipeline.json> [--input key=value ...]
`

// Run dispatches a CLI invocation and returns a process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "run":
		return runPipeline(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

func runPipeline(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	path := args[0]
	var inputArgs []string
	for i := 1; i < len(args); i++ {
		if args[i] == "--input" && i+1 < len(args) {
			inputArgs = append(inputArgs, args[i+1])
			i++
		}
	}
	input, err := parseInputs(inputArgs)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	p, err := loadPipelineFile(path)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if err := pipeline.Validate(p); err != nil {
		fmt.Fprintf(stderr, "invalid pipeline: %v\n", err)
		return 1
	}

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

	e := engine.New(engine.Deps{
		Config:    cfg,
		LLM:       agent.NewOllama(cfg.Ollama.BaseURL, http.DefaultClient),
		Search:    search.NewTavily(cfg.Search.BaseURL, cfg.APIKey(), http.DefaultClient),
		Fetcher:   fetch.NewHTTP(cfg.Fetch.UserAgent, cfg.Fetch.TimeoutSeconds, cfg.Fetch.MaxBytes),
		Artifacts: artifacts.NewDisk(defaultArtifactDir(cfg)),
		Store:     rowStore,
	})

	res, err := e.Run(context.Background(), p, input, nil)
	if err != nil {
		fmt.Fprintf(stderr, "run failed (%s): %v\n", res.Run.Status, err)
		return 1
	}
	fmt.Fprintf(stdout, "run %s: %s\n", res.Run.ID, res.Run.Status)
	for _, tr := range res.Traces {
		fmt.Fprintf(stdout, "  %-10s %-8s %s\n", tr.StepID, tr.Status, tr.OutputSummary)
	}
	return 0
}

func parseInputs(pairs []string) (map[string]any, error) {
	out := map[string]any{}
	for _, kv := range pairs {
		idx := strings.IndexByte(kv, '=')
		if idx < 0 {
			return nil, fmt.Errorf("invalid --input %q (want key=value)", kv)
		}
		out[kv[:idx]] = kv[idx+1:]
	}
	return out, nil
}

func loadPipelineFile(path string) (core.Pipeline, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return core.Pipeline{}, err
	}
	var p core.Pipeline
	if err := json.Unmarshal(b, &p); err != nil {
		return core.Pipeline{}, fmt.Errorf("decode pipeline: %w", err)
	}
	return p, nil
}

func defaultConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "fetch", "config.toml")
}

func defaultDBPath(cfg config.Config) string      { return filepath.Join(cfg.DataDir, "fetch.duckdb") }
func defaultArtifactDir(cfg config.Config) string { return filepath.Join(cfg.DataDir, "artifacts") }
