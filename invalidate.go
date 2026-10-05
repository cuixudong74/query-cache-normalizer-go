package qcache

import "sort"

// InvalidateResult reports a committed invalidation.
type InvalidateResult struct {
	Version           int64
	InvalidatedFields []string
	AffectedQueries   []string
	RemovedEntities   []string
}

// refsInValue collects every Ref reachable from a stored value.
func refsInValue(v any, into map[string]bool) {
	switch t := v.(type) {
	case Ref:
		into[t.ID] = true
	case []any:
		for _, e := range t {
			refsInValue(e, into)
		}
	case map[string]any:
		for _, e := range t {
			refsInValue(e, into)
		}
	}
}

// clearDangling removes Ref{id} and cleared root fields from every remaining
// entity field, replacing a direct ref with nothing (field deleted) and pruning
// refs inside lists/embedded maps. Missing keys collected drive affectedQueries.
func clearDangling(next *state, id string, missing map[string]bool) {
	root := next.entities[RootQueryID]
	for entID, e := range next.entities {
		for fname, f := range e.Fields {
			if f.Status != FieldPresent {
				continue
			}
			if r, ok := f.Value.(Ref); ok && r.ID == id {
				delete(e.Fields, fname)
				missing[entID+"."+fname] = true
				continue
			}
			pruned, changed := pruneRefs(f.Value, id)
			if changed {
				e.Fields[fname] = Field{Status: FieldPresent, Value: pruned}
				missing[entID+"."+fname] = true
			}
		}
	}
	// Root fields whose value was exactly the removed ref.
	for rk, f := range root.Fields {
		if r, ok := f.Value.(Ref); ok && r.ID == id {
			delete(root.Fields, rk)
			missing[RootQueryID+"."+rk] = true
		}
	}
}

// pruneRefs returns a copy of v with refs to id removed: list elements are
// dropped; map keys holding such a ref are deleted.
func pruneRefs(v any, id string) (any, bool) {
	switch t := v.(type) {
	case Ref:
		if t.ID == id {
			return nil, true
		}
		return t, false
	case []any:
		out := make([]any, 0, len(t))
		changed := false
		for _, e := range t {
			if r, ok := e.(Ref); ok && r.ID == id {
				changed = true
				continue
			}
			ne, cc := pruneRefs(e, id)
			if cc {
				changed = true
			}
			out = append(out, ne)
		}
		return out, changed
	case map[string]any:
		out := make(map[string]any, len(t))
		changed := false
		for k, e := range t {
			if r, ok := e.(Ref); ok && r.ID == id {
				changed = true
				continue
			}
			ne, cc := pruneRefs(e, id)
			if cc {
				changed = true
			}
			out[k] = ne
		}
		return out, changed
	default:
		return v, false
	}
}

// invalidateEntity removes an entity and all fields that reference it. Returns
// the normalized "ID.field" keys that became missing for affected-query calc.
func invalidateEntity(next *state, id string) map[string]bool {
	missing := map[string]bool{}
	if id == RootQueryID {
		return missing
	}
	if _, ok := next.entities[id]; ok {
		delete(next.entities, id)
		missing[id] = true
	}
	clearDangling(next, id, missing)
	return missing
}

// invalidateField clears one normalized field (key "EntityID.fieldName"). It
// also removes any root field by that storage name.
func invalidateField(next *state, id, field string) map[string]bool {
	missing := map[string]bool{}
	if e, ok := next.entities[id]; ok {
		if _, ok := e.Fields[field]; ok {
			delete(e.Fields, field)
			missing[id+"."+field] = true
		}
	}
	if id == RootQueryID {
		// Ensure a root field key clears even when the entity map view differs.
		if root, ok := next.entities[RootQueryID]; ok {
			if _, ok := root.Fields[field]; ok {
				delete(root.Fields, field)
				missing[RootQueryID+"."+field] = true
			}
		}
	}
	return missing
}

// gc reclaims entities and root fields no longer reachable from any registered
// query's root fields, returning removed store keys.
func gc(next *state) []string {
	root, ok := next.entities[RootQueryID]
	if !ok {
		return nil
	}
	// Live root field keys = union over registered queries' Root maps.
	liveRoot := map[string]bool{}
	for _, qi := range next.queries {
		for _, rk := range qi.Root {
			liveRoot[rk] = true
		}
	}
	// Sweep unreferenced root fields.
	removed := []string{}
	for rk := range root.Fields {
		if !liveRoot[rk] {
			delete(root.Fields, rk)
			removed = append(removed, RootQueryID+"."+rk)
		}
	}
	// DFS reachability over Refs, starting from live root field values.
	reachable := map[string]bool{RootQueryID: true}
	var visitValue func(v any)
	var visitEntity func(id string)
	visitValue = func(v any) {
		switch t := v.(type) {
		case Ref:
			visitEntity(t.ID)
		case []any:
			for _, e := range t {
				visitValue(e)
			}
		case map[string]any:
			for _, e := range t {
				visitValue(e)
			}
		}
	}
	visitEntity = func(id string) {
		if reachable[id] {
			return
		}
		reachable[id] = true
		e, ok := next.entities[id]
		if !ok {
			return
		}
		for _, f := range e.Fields {
			if f.Status == FieldPresent {
				visitValue(f.Value)
			}
		}
	}
	for rk := range liveRoot {
		if f, ok := root.Fields[rk]; ok && f.Status == FieldPresent {
			visitValue(f.Value)
		}
	}
	// Sweep unreachable entities (deterministic order).
	var ids []string
	for id := range next.entities {
		if id != RootQueryID && !reachable[id] {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		delete(next.entities, id)
		removed = append(removed, id)
	}
	return removed
}

// affectedQueries returns sorted fingerprints of queries whose rebuilt shape
// touches any of the given missing store keys (entities or "ID.field").
func affectedQueries(st *state, missing map[string]bool) []string {
	hit := map[string]bool{}
	for fp, qi := range st.queries {
		root, _ := st.entity(RootQueryID)
		for _, rk := range qi.Root {
			sel := findSelection(qi, rk)
			if missing[RootQueryID+"."+rk] {
				hit[fp] = true
			}
			if touches(st, root.Fields[rk], sel, missing) {
				hit[fp] = true
			}
		}
	}
	out := make([]string, 0, len(hit))
	for fp := range hit {
		out = append(out, fp)
	}
	sort.Strings(out)
	return out
}

// findSelection locates the top-level selection whose root key is rk.
func findSelection(qi *QueryInfo, rk string) *Selection {
	if qi == nil || qi.Query == nil {
		return nil
	}
	vars := qi.Query.Variables
	for i := range qi.Query.Selections {
		if rootFieldKey(qi.Query.Selections[i], vars) == rk {
			return &qi.Query.Selections[i]
		}
	}
	return nil
}

// touches reports whether a stored field value references a missing entity or
// a field on one.
func touches(st *state, f Field, sel *Selection, missing map[string]bool) bool {
	if f.Status != FieldPresent {
		return false
	}
	return valueTouches(st, f.Value, sel, missing)
}

func valueTouches(st *state, v any, sel *Selection, missing map[string]bool) bool {
	switch t := v.(type) {
	case Ref:
		if missing[t.ID] {
			return true
		}
		e, ok := st.entity(t.ID)
		if !ok {
			return false
		}
		sels := []Selection{}
		if sel != nil {
			sels = sel.Selections
		}
		for _, sub := range matchingSels(sels, e.Typename) {
			if missing[t.ID+"."+sub.ResponseName()] {
				return true
			}
			if f, ok := e.Fields[sub.ResponseName()]; ok {
				if valueTouches(st, f.Value, &sub, missing) {
					return true
				}
			}
		}
		return false
	case []any:
		for _, e := range t {
			if valueTouches(st, e, sel, missing) {
				return true
			}
		}
		return false
	case map[string]any:
		for _, e := range t {
			if valueTouches(st, e, sel, missing) {
				return true
			}
		}
		return false
	default:
		return false
	}
}
