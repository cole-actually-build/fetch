package pipeline

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cole/fetch/internal/core"
)

// Repository persists pipeline definitions as JSON files under a directory.
type Repository struct {
	dir string
}

// NewRepository roots the repository at {dataDir}/pipelines.
func NewRepository(dataDir string) *Repository {
	return &Repository{dir: filepath.Join(dataDir, "pipelines")}
}

func (r *Repository) path(id string) string {
	return filepath.Join(r.dir, id+".json")
}

// Save writes the pipeline as indented JSON, creating the directory.
func (r *Repository) Save(p core.Pipeline) error {
	if p.ID == "" {
		return fmt.Errorf("cannot save pipeline with empty id")
	}
	if err := os.MkdirAll(r.dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(r.path(p.ID), b, 0o644)
}

// Load reads one pipeline by ID.
func (r *Repository) Load(id string) (core.Pipeline, error) {
	b, err := os.ReadFile(r.path(id))
	if err != nil {
		return core.Pipeline{}, err
	}
	var p core.Pipeline
	if err := json.Unmarshal(b, &p); err != nil {
		return core.Pipeline{}, fmt.Errorf("decode pipeline %q: %w", id, err)
	}
	return p, nil
}

// List loads every pipeline in the directory (missing dir → empty list).
func (r *Repository) List() ([]core.Pipeline, error) {
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []core.Pipeline
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		p, err := r.Load(id)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// Delete removes one pipeline definition.
func (r *Repository) Delete(id string) error {
	return os.Remove(r.path(id))
}
