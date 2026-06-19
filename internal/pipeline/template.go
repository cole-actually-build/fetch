package pipeline

import (
	"fmt"
	"regexp"
	"strings"
)

// Scope holds the values references can resolve against during a run.
type Scope struct {
	Input map[string]any
	Steps map[string]map[string]any
}

var refRe = regexp.MustCompile(`{{\s*([a-zA-Z0-9_.]+)\s*}}`)

// Resolve walks params and replaces {{...}} references using sc.
func Resolve(params map[string]any, sc Scope) (map[string]any, error) {
	out := make(map[string]any, len(params))
	for k, v := range params {
		rv, err := resolveValue(v, sc)
		if err != nil {
			return nil, err
		}
		out[k] = rv
	}
	return out, nil
}

func resolveValue(v any, sc Scope) (any, error) {
	switch t := v.(type) {
	case string:
		return resolveString(t, sc)
	case []any:
		res := make([]any, len(t))
		for i, e := range t {
			rv, err := resolveValue(e, sc)
			if err != nil {
				return nil, err
			}
			res[i] = rv
		}
		return res, nil
	case map[string]any:
		return Resolve(t, sc)
	default:
		return v, nil
	}
}

func resolveString(s string, sc Scope) (any, error) {
	trimmed := strings.TrimSpace(s)
	if m := refRe.FindStringSubmatch(trimmed); m != nil && m[0] == trimmed {
		val, ok := lookup(m[1], sc)
		if !ok {
			return nil, fmt.Errorf("unresolved reference %q", m[1])
		}
		return val, nil
	}
	var firstErr error
	res := refRe.ReplaceAllStringFunc(s, func(tok string) string {
		name := refRe.FindStringSubmatch(tok)[1]
		val, ok := lookup(name, sc)
		if !ok {
			if firstErr == nil {
				firstErr = fmt.Errorf("unresolved reference %q", name)
			}
			return ""
		}
		return fmt.Sprintf("%v", val)
	})
	if firstErr != nil {
		return nil, firstErr
	}
	return res, nil
}

func lookup(name string, sc Scope) (any, bool) {
	parts := strings.Split(name, ".")
	switch parts[0] {
	case "input":
		if len(parts) != 2 {
			return nil, false
		}
		v, ok := sc.Input[parts[1]]
		return v, ok
	case "steps":
		if len(parts) != 3 {
			return nil, false
		}
		fields, ok := sc.Steps[parts[1]]
		if !ok {
			return nil, false
		}
		v, ok := fields[parts[2]]
		return v, ok
	}
	return nil, false
}
