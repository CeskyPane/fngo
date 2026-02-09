package party

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

func normalizeMetaMap(meta map[string]any) map[string]string {
	out := make(map[string]string, len(meta))
	for key, value := range meta {
		s, ok := value.(string)
		if ok {
			out[key] = s
			continue
		}

		raw, err := json.Marshal(value)
		if err != nil {
			out[key] = ""
			continue
		}

		out[key] = string(raw)
	}

	return out
}

func decodeJSONMap(raw string) map[string]any {
	if strings.TrimSpace(raw) == "" {
		return map[string]any{}
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return map[string]any{}
	}

	if out == nil {
		return map[string]any{}
	}

	return out
}

func decodeJSONMapValue(src map[string]any, key string) map[string]any {
	if src == nil {
		return map[string]any{}
	}

	current, ok := src[key]
	if !ok {
		return map[string]any{}
	}

	asMap, ok := current.(map[string]any)
	if ok {
		return asMap
	}

	raw, err := json.Marshal(current)
	if err != nil {
		return map[string]any{}
	}

	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return map[string]any{}
	}

	if parsed == nil {
		return map[string]any{}
	}

	return parsed
}

func serializeMetaValue(key string, value any) string {
	if strings.HasSuffix(key, "_j") {
		s, ok := value.(string)
		if ok {
			return s
		}

		raw, err := json.Marshal(value)
		if err != nil {
			return "{}"
		}

		return string(raw)
	}

	if strings.HasSuffix(key, "_b") {
		b, ok := value.(bool)
		if ok {
			if b {
				return "true"
			}

			return "false"
		}
	}

	s, ok := value.(string)
	if ok {
		return s
	}

	return fmt.Sprint(value)
}

func decodeMetaSchema(schema map[string]string) map[string]any {
	out := make(map[string]any, len(schema))
	for key, raw := range schema {
		out[key] = decodeMetaValue(key, raw)
	}

	return out
}

func decodeMetaValue(key, raw string) any {
	if strings.HasSuffix(key, "_j") {
		parsed := decodeJSONMap(raw)
		if len(parsed) > 0 {
			return parsed
		}

		if strings.TrimSpace(raw) == "" {
			return map[string]any{}
		}

		return raw
	}

	if strings.HasSuffix(key, "_b") {
		if strings.EqualFold(strings.TrimSpace(raw), "true") {
			return true
		}

		if strings.EqualFold(strings.TrimSpace(raw), "false") {
			return false
		}
	}

	if strings.HasSuffix(key, "_U") {
		if n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64); err == nil {
			return n
		}
	}

	return raw
}

func cloneMetaStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return map[string]string{}
	}

	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}

	return out
}

func cloneStringSlice(in []string) []string {
	if len(in) == 0 {
		return []string{}
	}

	out := make([]string, len(in))
	copy(out, in)

	return out
}

func buildMetaPatch(updates map[string]any) map[string]string {
	out := make(map[string]string, len(updates))
	for key, value := range updates {
		out[key] = serializeMetaValue(key, value)
	}

	return out
}
