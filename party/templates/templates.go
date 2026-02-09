package templates

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

var (
	//go:embed defaultPartyMeta.json
	defaultPartyMetaRaw []byte

	//go:embed defaultPartyMemberMeta.json
	defaultPartyMemberMetaRaw []byte

	templatesOnce sync.Once
	templatesErr  error
	partyMeta     map[string]any
	memberMeta    map[string]any
)

func DefaultPartyMeta() map[string]any {
	ensureTemplates()

	if templatesErr != nil {
		return map[string]any{}
	}

	return deepCopyMap(partyMeta)
}

func DefaultPartyMemberMeta() map[string]any {
	ensureTemplates()

	if templatesErr != nil {
		return map[string]any{}
	}

	return deepCopyMap(memberMeta)
}

func DeepMerge(dst, src map[string]any) map[string]any {
	if dst == nil {
		dst = map[string]any{}
	}

	for key, srcValue := range src {
		dstValue, exists := dst[key]
		if !exists {
			dst[key] = deepCopyValue(srcValue)
			continue
		}

		srcMap, srcOK := srcValue.(map[string]any)
		dstMap, dstOK := dstValue.(map[string]any)
		if srcOK && dstOK {
			dst[key] = DeepMerge(dstMap, srcMap)
			continue
		}

		dst[key] = deepCopyValue(srcValue)
	}

	return dst
}

func PathGet(root map[string]any, path string) (any, bool) {
	if root == nil {
		return nil, false
	}

	parts := splitPath(path)
	if len(parts) == 0 {
		return nil, false
	}

	current := any(root)
	for idx, part := range parts {
		asMap, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}

		next, exists := asMap[part]
		if !exists {
			return nil, false
		}

		if idx == len(parts)-1 {
			return next, true
		}

		current = next
	}

	return nil, false
}

func PathSet(root map[string]any, path string, value any) bool {
	if root == nil {
		return false
	}

	parts := splitPath(path)
	if len(parts) == 0 {
		return false
	}

	current := root
	for idx := 0; idx < len(parts)-1; idx++ {
		part := parts[idx]
		next, exists := current[part]
		if !exists {
			nextMap := map[string]any{}
			current[part] = nextMap
			current = nextMap
			continue
		}

		asMap, ok := next.(map[string]any)
		if !ok {
			return false
		}

		current = asMap
	}

	current[parts[len(parts)-1]] = deepCopyValue(value)

	return true
}

func MustJSONMap(raw string) map[string]any {
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		panic(fmt.Sprintf("templates: invalid json map: %v", err))
	}

	if out == nil {
		return map[string]any{}
	}

	return out
}

func ensureTemplates() {
	templatesOnce.Do(func() {
		partyMeta, templatesErr = parseTemplate(defaultPartyMetaRaw)
		if templatesErr != nil {
			return
		}

		memberMeta, templatesErr = parseTemplate(defaultPartyMemberMetaRaw)
	})
}

func parseTemplate(raw []byte) (map[string]any, error) {
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}

	if out == nil {
		out = map[string]any{}
	}

	return out, nil
}

func deepCopyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return map[string]any{}
	}

	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = deepCopyValue(value)
	}

	return out
}

func deepCopySlice(in []any) []any {
	if len(in) == 0 {
		return []any{}
	}

	out := make([]any, len(in))
	for idx, value := range in {
		out[idx] = deepCopyValue(value)
	}

	return out
}

func deepCopyValue(v any) any {
	switch typed := v.(type) {
	case map[string]any:
		return deepCopyMap(typed)
	case []any:
		return deepCopySlice(typed)
	default:
		return typed
	}
}

func splitPath(path string) []string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return nil
	}

	parts := strings.Split(trimmed, ".")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil
		}

		out = append(out, part)
	}

	return out
}
