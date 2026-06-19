package engine

import (
	"encoding/json"
	"strings"

	"github.com/cole/fetch/internal/core"
)

func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}

// toStringSlice accepts []string, []any of strings, or a single string.
func toStringSlice(v any) ([]string, error) {
	switch t := v.(type) {
	case nil:
		return nil, nil
	case []string:
		return t, nil
	case string:
		return []string{t}, nil
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			s, ok := e.(string)
			if !ok {
				return nil, errInvalidStringList
			}
			out = append(out, s)
		}
		return out, nil
	default:
		return nil, errInvalidStringList
	}
}

// toRows accepts []map[string]any or []any of map[string]any.
func toRows(v any) []map[string]any {
	switch t := v.(type) {
	case []map[string]any:
		return t
	case []any:
		out := make([]map[string]any, 0, len(t))
		for _, e := range t {
			if m, ok := e.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	default:
		return nil
	}
}

func artifactExt(contentType string) string {
	switch {
	case strings.Contains(contentType, "html"):
		return "html"
	case strings.Contains(contentType, "json"):
		return "json"
	default:
		return "txt"
	}
}

func dedupRows(rows []map[string]any, by []string) []map[string]any {
	seen := map[string]bool{}
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		key := rowKey(r, by)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, r)
	}
	return out
}

func rowKey(r map[string]any, by []string) string {
	if len(by) == 0 {
		b, _ := json.Marshal(r)
		return string(b)
	}
	parts := make([]string, len(by))
	for i, k := range by {
		b, _ := json.Marshal(r[k])
		parts[i] = string(b)
	}
	return strings.Join(parts, "\x00")
}

func applyRevisions(p core.Pipeline, revs []Revision) core.Pipeline {
	out := p
	out.Plan = make([]core.Step, len(p.Plan))
	copy(out.Plan, p.Plan)
	for _, rev := range revs {
		for i, s := range out.Plan {
			if s.ID == rev.StepID {
				out.Plan[i] = rev.Adapted
			}
		}
	}
	return out
}

var errInvalidStringList = &engineError{"expected a list of strings"}

type engineError struct{ msg string }

func (e *engineError) Error() string { return e.msg }
