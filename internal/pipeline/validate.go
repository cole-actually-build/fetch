// Package pipeline owns the durable pipeline definition: persistence,
// validation, dependency ordering, and parameter templating.
package pipeline

import (
	"errors"
	"fmt"

	"github.com/cole/fetch/internal/core"
)

// Validate checks a pipeline is well-formed enough to execute.
func Validate(p core.Pipeline) error {
	if p.ID == "" {
		return errors.New("pipeline id is empty")
	}
	if len(p.Plan) == 0 {
		return errors.New("pipeline has no steps")
	}
	for _, s := range p.Plan {
		switch s.Type {
		case core.StepSearch, core.StepFetch, core.StepExtract, core.StepTransform, core.StepStore:
		default:
			return fmt.Errorf("step %q has invalid type %q", s.ID, s.Type)
		}
	}
	if _, err := TopoOrder(p); err != nil {
		return err
	}
	return nil
}

// TopoOrder returns the steps in dependency order using Kahn's algorithm,
// preserving plan order among ready nodes. It errors on duplicate step IDs,
// dependencies on unknown steps, or cycles.
func TopoOrder(p core.Pipeline) ([]core.Step, error) {
	byID := make(map[string]core.Step, len(p.Plan))
	indeg := make(map[string]int, len(p.Plan))
	for _, s := range p.Plan {
		if _, dup := byID[s.ID]; dup {
			return nil, fmt.Errorf("duplicate step id %q", s.ID)
		}
		byID[s.ID] = s
		indeg[s.ID] = 0
	}
	adj := make(map[string][]string)
	for _, s := range p.Plan {
		for _, dep := range s.DependsOn {
			if _, ok := byID[dep]; !ok {
				return nil, fmt.Errorf("step %q depends on unknown step %q", s.ID, dep)
			}
			adj[dep] = append(adj[dep], s.ID)
			indeg[s.ID]++
		}
	}
	var queue []string
	for _, s := range p.Plan { // plan order among ready nodes
		if indeg[s.ID] == 0 {
			queue = append(queue, s.ID)
		}
	}
	order := make([]core.Step, 0, len(p.Plan))
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		order = append(order, byID[id])
		for _, nb := range adj[id] {
			indeg[nb]--
			if indeg[nb] == 0 {
				queue = append(queue, nb)
			}
		}
	}
	if len(order) != len(p.Plan) {
		return nil, errors.New("plan has a dependency cycle")
	}
	return order, nil
}
