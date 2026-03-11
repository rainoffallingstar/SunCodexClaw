package wizard

import "strings"

func ensureMap(root map[string]any, key string) map[string]any {
	if v, ok := root[key]; ok {
		if m, ok := v.(map[string]any); ok {
			return m
		}
	}
	m := map[string]any{}
	root[key] = m
	return m
}

func hasDotted(root map[string]any, dotted string) bool {
	return !isEmpty(getDotted(root, dotted))
}

func isEmpty(v any) bool {
	if v == nil {
		return true
	}
	switch tv := v.(type) {
	case string:
		return strings.TrimSpace(tv) == ""
	case []any:
		return len(tv) == 0
	}
	return false
}

func getDotted(root map[string]any, dotted string) any {
	parts := strings.Split(dotted, ".")
	var cur any = root
	for _, p := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[p]
	}
	return cur
}
