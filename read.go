package qcache

// ReadResult is the rebuilt result of one query.
type ReadResult struct {
	Data     map[string]any
	Complete bool
	Missing  []string // normalized paths that were absent (missing, not null)
	Skipped  []string // normalized paths skipped due to an error
	Version  int64
}

// rebuild denormalizes a registered query out of a committed state. Missing
// fields are emitted as MissingMarker, skipped fields as SkippedMarker; an
// explicit cached null stays nil so absence is never confused with null.
func rebuild(st *state, qi *QueryInfo) ReadResult {
	res := ReadResult{Version: st.seq, Data: map[string]any{}}
	complete := true
	root, _ := st.entity(RootQueryID)

	for _, sel := range qi.Query.Selections {
		rk := qi.Root[sel.ResponseName()]
		if rk == "" {
			rk = rootFieldKey(sel, qi.Query.Variables)
		}
		f, ok := root.Fields[rk]
		if !ok {
			res.Data[sel.ResponseName()] = MissingMarker{Reason: "field " + rk + " not cached"}
			res.Missing = append(res.Missing, RootQueryID+"."+rk)
			complete = false
			continue
		}
		v, c := readFieldValue(st, f, sel, RootQueryID+"."+rk, &res)
		if !c {
			complete = false
		}
		res.Data[sel.ResponseName()] = v
	}
	res.Complete = complete
	return res
}

// readFieldValue renders one stored Field under its selection. It returns
// complete=false when the field is skipped or its subtree has holes.
func readFieldValue(st *state, f Field, sel Selection, path string, res *ReadResult) (any, bool) {
	if f.Status == FieldSkipped {
		err := FieldError{}
		if f.Err != nil {
			err = *f.Err
		}
		res.Skipped = append(res.Skipped, path)
		return SkippedMarker{Error: err}, false
	}
	return readStoredValue(st, f.Value, sel, path, res)
}

// readStoredValue renders a stored normalized value plus whether that subtree
// is complete. Refs are resolved against the entity store.
func readStoredValue(st *state, v any, sel Selection, path string, res *ReadResult) (any, bool) {
	switch t := v.(type) {
	case nil:
		return nil, true
	case Ref:
		return readRef(st, t, sel, path, res)
	case []any:
		out := make([]any, len(t))
		complete := true
		for i, elem := range t {
			ep := joinPath(path, i)
			var cv any
			var cc bool
			if r, ok := elem.(Ref); ok {
				cv, cc = readRef(st, r, sel, ep, res)
			} else {
				cv, cc = readStoredValue(st, elem, sel, ep, res)
			}
			out[i] = cv
			if !cc {
				complete = false
			}
		}
		return out, complete
	case map[string]any:
		return readEmbedded(st, t, sel.Selections, path, res)
	default:
		if len(sel.Selections) > 0 {
			res.Missing = append(res.Missing, path)
			return MissingMarker{Reason: "scalar where object selected"}, false
		}
		return v, true
	}
}

// readRef resolves an entity reference under the selection's sub-selections.
func readRef(st *state, r Ref, sel Selection, path string, res *ReadResult) (any, bool) {
	if r.ID == RootQueryID {
		res.Missing = append(res.Missing, path)
		return MissingMarker{Reason: "unexpected root reference"}, false
	}
	e, ok := st.entity(r.ID)
	if !ok {
		// Dangling reference (entity invalidated/GCd): distinguishable hole.
		res.Missing = append(res.Missing, path)
		return MissingMarker{Reason: "referenced entity " + r.ID + " unavailable"}, false
	}
	return readEntity(st, r.ID, e, sel.Selections, path, res)
}

// readEntity projects an entity's stored fields through selections, honoring
// union/interface fragment conditions against its runtime typename.
func readEntity(st *state, id string, e *Entity, sels []Selection, path string, res *ReadResult) (map[string]any, bool) {
	active := matchingSels(sels, e.Typename)
	out := map[string]any{}
	complete := true
	for _, sub := range active {
		fp := joinPath(path, sub.ResponseName())
		f, ok := e.Fields[sub.ResponseName()]
		if !ok {
			out[sub.ResponseName()] = MissingMarker{Reason: "field " + sub.ResponseName() + " not cached"}
			res.Missing = append(res.Missing, id+"."+sub.ResponseName())
			complete = false
			continue
		}
		v, c := readFieldValue(st, f, sub, fp, res)
		if !c {
			complete = false
		}
		out[sub.ResponseName()] = v
	}
	return out, complete
}

// readEmbedded projects an identity-less stored map through selections.
func readEmbedded(st *state, m map[string]any, sels []Selection, path string, res *ReadResult) (map[string]any, bool) {
	out := map[string]any{}
	complete := true
	for _, sub := range sels {
		if sub.TypeName != "" {
			// Embedded objects carry no fragment type info; skip conditions.
			continue
		}
		fp := joinPath(path, sub.ResponseName())
		v, ok := m[sub.ResponseName()]
		if !ok {
			out[sub.ResponseName()] = MissingMarker{Reason: "field " + sub.ResponseName() + " not cached"}
			res.Missing = append(res.Missing, fp)
			complete = false
			continue
		}
		if fe, isSkip := asSkipped(v); isSkip {
			out[sub.ResponseName()] = SkippedMarker{Error: fe}
			res.Skipped = append(res.Skipped, fp)
			complete = false
			continue
		}
		cv, cc := readStoredValue(st, v, sub, fp, res)
		out[sub.ResponseName()] = cv
		if !cc {
			complete = false
		}
	}
	return out, complete
}
