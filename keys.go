package qcache

import (
	"bytes"
	"encoding/json"
	"sort"
)

// canonicalJSON returns a deterministic JSON encoding of v with object keys
// sorted. It is used for stable root field keys, query fingerprints and
// persisted output, which makes identical operation sequences produce
// byte-identical observable results.
func canonicalJSON(v any) string {
	var b bytes.Buffer
	writeCanonical(&b, v)
	return b.String()
}

func writeCanonical(b *bytes.Buffer, v any) {
	switch t := v.(type) {
	case nil:
		b.WriteString("null")
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			kb, _ := json.Marshal(k)
			b.Write(kb)
			b.WriteByte(':')
			writeCanonical(b, t[k])
		}
		b.WriteByte('}')
	case []any:
		b.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				b.WriteByte(',')
			}
			writeCanonical(b, e)
		}
		b.WriteByte(']')
	case Ref:
		writeCanonical(b, map[string]any{"$ref": t.ID})
	case Variable:
		writeCanonical(b, map[string]any{"$var": t.Name})
	default:
		// json.Marshal covers string, bool, numbers and other json-able types
		// with stable formatting.
		raw, err := json.Marshal(normalizeNumber(t))
		if err != nil {
			// Fall back to a string key rather than panicking; callers only
			// feed JSON-compatible values in practice.
			rb, _ := json.Marshal(err.Error())
			b.Write(rb)
			return
		}
		b.Write(raw)
	}
}

// normalizeNumber canonicalizes integer-valued floats so that 1 and 1.0 share
// a key; other floats are left as float64.
func normalizeNumber(v any) any {
	if f, ok := v.(float64); ok && f == float64(int64(f)) {
		return int64(f)
	}
	return v
}

// resolveArgs substitutes Variable placeholders in args from variables, walking
// nested maps and slices. Non-variable values are copied through unchanged.
func resolveArgs(args map[string]any, variables map[string]any) map[string]any {
	if len(args) == 0 {
		return nil
	}
	out := make(map[string]any, len(args))
	for k, v := range args {
		out[k] = resolveValue(v, variables)
	}
	return out
}

func resolveValue(v any, variables map[string]any) any {
	switch t := v.(type) {
	case Variable:
		if val, ok := variables[t.Name]; ok {
			return val
		}
		return nil
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, e := range t {
			m[k] = resolveValue(e, variables)
		}
		return m
	case []any:
		s := make([]any, len(t))
		for i, e := range t {
			s[i] = resolveValue(e, variables)
		}
		return s
	default:
		return v
	}
}

// rootFieldKey is the ROOT_QUERY storage field name for a top-level selection:
// arguments are part of the identity, so user(id:1) and user(id:2) never
// collide, while the response alias is not part of the key.
func rootFieldKey(sel Selection, variables map[string]any) string {
	args := resolveArgs(sel.Args, variables)
	if len(args) == 0 {
		return sel.Name
	}
	return sel.Name + "(" + canonicalJSON(args) + ")"
}

// queryFingerprint returns a stable identity for a bound query. It is based on
// the operation name and resolved variables; when unnamed, the canonical
// selection document is used. Alias differences between two queries therefore
// never split the fingerprint, while different variables do.
func queryFingerprint(q *Query) string {
	if q.Name != "" {
		return q.Name + "|" + canonicalJSON(q.Variables)
	}
	return canonicalJSON(map[string]any{
		"type":       string(q.Type),
		"variables":  q.Variables,
		"selections": q.Selections,
	})
}
