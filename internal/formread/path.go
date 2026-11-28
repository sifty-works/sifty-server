package formread

import (
	"encoding/json"
	"strings"
)

func walk(v any, path string, decode string) []any {
	if path == "" {
		return []any{v}
	}
	key, rest, _ := strings.Cut(path, ".")
	switch x := v.(type) {
	case string:
		if decode != "" {
			if decoded, err := decoders[decode](x); err == nil {
				return walk(decoded, path, decode)
			}
		}
	case []any:
		var out []any
		for _, item := range x {
			out = append(out, walk(item, path, decode)...)
		}
		return out
	case map[string]any:
		if next, ok := x[key]; ok {
			return walk(next, rest, decode)
		}
	}
	return nil
}

func first(v any, path, decode string) any {
	if r := walk(v, path, decode); len(r) > 0 {
		return r[0]
	}
	return nil
}

func text(v any) string {
	var s string
	switch x := v.(type) {
	case string:
		s = x
	case json.Number:
		s = x.String()
	}
	return strings.Join(strings.Fields(s), " ")
}
