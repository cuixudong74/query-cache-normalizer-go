package qcache

// Ref is a normalized reference to another entity. Embedded in stored values
// it is replaced by the referenced entity's selected fields on read.
type Ref struct {
	ID string `json:"$ref"`
}

// Variable is a placeholder in query args, substituted from Query.Variables
// when the query is bound.
type Variable struct {
	Name string `json:"$var"`
}

// IsRef reports whether v is a normalized reference.
func IsRef(v any) bool {
	_, ok := v.(Ref)
	return ok
}

// embeddedSkipped is the storage-layer sentinel for a skipped field inside an
// identity-less (embedded) object. It never escapes to readers: rebuild turns
// it into the public SkippedMarker.
type embeddedSkipped struct {
	Err FieldError
}

// asSkipped returns the skipped error when v is the sentinel.
func asSkipped(v any) (FieldError, bool) {
	if s, ok := v.(embeddedSkipped); ok {
		return s.Err, true
	}
	return FieldError{}, false
}

// cloneValue deep-copies a normalized value (maps, slices and Refs) so that a
// copy-on-write state never shares mutable containers with its parent snapshot.
func cloneValue(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case Ref:
		return Ref{ID: t.ID}
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, e := range t {
			m[k] = cloneValue(e)
		}
		return m
	case []any:
		s := make([]any, len(t))
		for i, e := range t {
			s[i] = cloneValue(e)
		}
		return s
	default:
		// Primitives (string, bool, numbers) are immutable and shared safely.
		return v
	}
}
