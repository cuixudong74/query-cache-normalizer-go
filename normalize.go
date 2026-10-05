package qcache

import (
	"strconv"
)

// pathElem renders one path element the same way error paths and local paths
// are rendered, so list indices encoded as numbers compare correctly.
func pathElem(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return canonicalJSON(normalizeNumber(t))
	default:
		return canonicalJSON(t)
	}
}

// joinPath extends a "/"-separated response path by one element.
func joinPath(prefix string, elem any) string {
	e := pathElem(elem)
	if prefix == "" {
		return e
	}
	return prefix + "/" + e
}

// indexByPath maps each error path to its FieldError.
func indexByPath(errs []FieldError) map[string]FieldError {
	idx := map[string]FieldError{}
	for _, e := range errs {
		p := ""
		for _, el := range e.Path {
			p = joinPath(p, el)
		}
		idx[p] = e
	}
	return idx
}

// errorAt returns the error located exactly at path, if any.
func errorAt(idx map[string]FieldError, path string) (FieldError, bool) {
	e, ok := idx[path]
	return e, ok
}

// ancestorError returns an error at path or any of its ancestors. GraphQL
// locates failures on the field closest to the problem, and a failing parent
// implies its children are absent.
func ancestorError(idx map[string]FieldError, path string) (FieldError, bool) {
	if e, ok := idx[path]; ok {
		return e, true
	}
	for i := 0; i < len(path); i++ {
		if path[i] != '/' {
			continue
		}
		if e, ok := idx[path[:i]]; ok {
			return e, true
		}
	}
	return FieldError{}, false
}

// matchingSels applies inline-fragment type conditions for the runtime
// typename. Unconditional selections always apply; conditional ones apply only
// to the concrete type, which keeps union/interface writes from polluting
// fields that belong to another variant.
func matchingSels(sels []Selection, typename string) []Selection {
	out := make([]Selection, 0, len(sels))
	for _, s := range sels {
		if s.TypeName == "" || s.TypeName == typename {
			out = append(out, s)
		}
	}
	return out
}

// runtimeTypename extracts __typename from an object when present.
func runtimeTypename(data map[string]any) string {
	if t, ok := data["__typename"].(string); ok {
		return t
	}
	return ""
}

// normalize merges a bound response into next (a copy-on-write clone of the
// current state), binds root fields, and reports whether the rebuilt query is
// complete. Fields absent from a partial response are simply not written, so
// previously cached fields survive; skipped fields are recorded explicitly.
func normalize(cfg Config, next *state, qi *QueryInfo, resp *Response, errIndex map[string]FieldError) bool {
	vars := map[string]any{}
	if qi.Query != nil {
		vars = qi.Query.Variables
	}
	root := next.ensureEntity(RootQueryID, "Query")

	if qi.Root == nil {
		qi.Root = map[string]string{}
	}
	for _, sel := range qi.Query.Selections {
		rk := rootFieldKey(sel, vars)
		qi.Root[sel.ResponseName()] = rk

		path := sel.ResponseName()
		raw, present := resp.Data[sel.ResponseName()]
		if !present {
			// Absent from this response: leave any existing cached value in
			// place (merge). A co-located error still records a skip.
			if e, ok := errorAt(errIndex, path); ok {
				root.setField(rk, Field{Status: FieldSkipped, Err: &e})
			}
			continue
		}
		if e, ok := errorAt(errIndex, path); ok {
			root.setField(rk, Field{Status: FieldSkipped, Err: &e})
			continue
		}
		val := writeValue(cfg, next, raw, sel, path, errIndex)
		root.setField(rk, Field{Status: FieldPresent, Value: val})
	}

	res := rebuild(next, qi)
	return res.Complete && len(res.Missing) == 0 && len(res.Skipped) == 0
}

// writeValue normalizes one raw response value for a selection, producing the
// value stored on the parent field: a Ref for identified objects, an embedded
// map for identity-less objects, or a slice/primitive/null.
func writeValue(cfg Config, next *state, raw any, sel Selection, path string, errIndex map[string]FieldError) any {
	if len(sel.Selections) == 0 {
		return cloneValue(raw)
	}
	switch v := raw.(type) {
	case nil:
		return nil
	case map[string]any:
		return writeObjectValue(cfg, next, v, sel.Selections, path, errIndex)
	case []any:
		out := make([]any, len(v))
		for i, elem := range v {
			ep := joinPath(path, i)
			if elem == nil {
				out[i] = nil
				continue
			}
			if _, ok := elem.(map[string]any); !ok {
				out[i] = cloneValue(elem)
				continue
			}
			if e, ok := ancestorError(errIndex, ep); ok {
				_ = e
				// An object-level error with no usable value: keep a skipped
				// marker only when the object itself is absent. The element
				// exists here, so normalize whatever fields were returned;
				// missing children become skipped via their own paths.
			}
			out[i] = writeObjectValue(cfg, next, elem.(map[string]any), sel.Selections, ep, errIndex)
		}
		return out
	default:
		return cloneValue(raw)
	}
}

// writeObjectValue normalizes one object and returns its stored representation
// (Ref when identifiable, embedded map otherwise).
func writeObjectValue(cfg Config, next *state, data map[string]any, sels []Selection, path string, errIndex map[string]FieldError) any {
	tn := runtimeTypename(data)
	active := matchingSels(sels, tn)

	if id, ok := cfg.identityOf(tn, data); ok {
		e := next.ensureEntity(id, tn)
		for _, sub := range active {
			fp := joinPath(path, sub.ResponseName())
			raw, present := data[sub.ResponseName()]
			if !present {
				if fe, ok := errorAt(errIndex, fp); ok {
					e.setField(sub.ResponseName(), Field{Status: FieldSkipped, Err: &fe})
				}
				continue
			}
			if fe, ok := errorAt(errIndex, fp); ok {
				e.setField(sub.ResponseName(), Field{Status: FieldSkipped, Err: &fe})
				continue
			}
			e.setField(sub.ResponseName(), Field{
				Status: FieldPresent,
				Value:  writeValue(cfg, next, raw, sub, fp, errIndex),
			})
		}
		return Ref{ID: id}
	}

	// Embedded (identity-less) object: stored inline as a normalized map.
	out := map[string]any{}
	for _, sub := range active {
		fp := joinPath(path, sub.ResponseName())
		raw, present := data[sub.ResponseName()]
		if !present {
			if fe, ok := errorAt(errIndex, fp); ok {
				out[sub.ResponseName()] = embeddedSkipped{Err: fe}
			}
			continue
		}
		if fe, ok := errorAt(errIndex, fp); ok {
			out[sub.ResponseName()] = embeddedSkipped{Err: fe}
			continue
		}
		out[sub.ResponseName()] = writeValue(cfg, next, raw, sub, fp, errIndex)
	}
	return out
}
