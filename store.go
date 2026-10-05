package qcache

func newState() *state {
	return &state{
		entities: map[string]*Entity{
			RootQueryID: {Typename: "Query", Fields: map[string]Field{}},
		},
		queries: map[string]*QueryInfo{},
	}
}

// clone produces a copy-on-write copy: the state and its entity/query maps are
// duplicated, but entity records and values are shared until actually mutated.
// Fields are deep-cloned when written through ensureEntity/setField.
func (s *state) clone() *state {
	ns := &state{
		seq:      s.seq,
		entities: make(map[string]*Entity, len(s.entities)),
		queries:  make(map[string]*QueryInfo, len(s.queries)),
	}
	for k, e := range s.entities {
		cp := &Entity{Typename: e.Typename, Fields: make(map[string]Field, len(e.Fields))}
		for fk, fv := range e.Fields {
			cp.Fields[fk] = fv // values remain shared; cloneValue on writes
		}
		ns.entities[k] = cp
	}
	for k, q := range s.queries {
		nq := &QueryInfo{Complete: q.Complete, Root: make(map[string]string, len(q.Root))}
		for rk, rv := range q.Root {
			nq.Root[rk] = rv
		}
		nq.Query = q.Query // query documents are treated as immutable
		ns.queries[k] = nq
	}
	return ns
}

func (s *state) entity(id string) (*Entity, bool) {
	e, ok := s.entities[id]
	return e, ok
}

// ensureEntity returns the entity, creating it when absent. A previously
// embedded-created record keeps its first-seen typename.
func (s *state) ensureEntity(id, typename string) *Entity {
	if e, ok := s.entities[id]; ok {
		if e.Typename == "" && typename != "" {
			e.Typename = typename
		}
		return e
	}
	e := &Entity{Typename: typename, Fields: map[string]Field{}}
	s.entities[id] = e
	return e
}

// setField records one field with the deterministic conflict rule:
//   - A present value (including an explicit null) overwrites an older present
//     value: last committed write wins, so the newest server truth stabilizes.
//   - A skipped value never overwrites a present value: partial failures must
//     not erase good data.
//   - A present value does replace a previously skipped marker.
func (e *Entity) setField(name string, f Field) {
	if f.Status == FieldPresent {
		f.Value = cloneValue(f.Value)
		e.Fields[name] = f
		return
	}
	if old, ok := e.Fields[name]; ok && old.Status == FieldPresent {
		return
	}
	e.Fields[name] = f
}
