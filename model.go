package qcache

// FieldStatus distinguishes a cached present value from a value skipped
// because the server returned an error for that path.
type FieldStatus string

const (
	// FieldPresent: a usable (possibly null) value was returned.
	FieldPresent FieldStatus = "present"
	// FieldSkipped: the server returned an error for this field without a
	// usable value.
	FieldSkipped FieldStatus = "skipped"
)

// Field is one stored field of an entity.
type Field struct {
	Status FieldStatus
	// Value is the stored value for FieldPresent (may legitimately be nil,
	// which denotes an explicit null). Undefined for FieldSkipped.
	Value any
	// Err is the error that caused FieldSkipped.
	Err *FieldError
}

// Entity is the normalized record for one identity.
type Entity struct {
	Typename string
	Fields   map[string]Field
}

// QueryInfo records one registered query so that the cache can rebuild its
// result shape, compute affected queries and garbage collect.
type QueryInfo struct {
	// Query is the bound (variables-resolved) query document.
	Query *Query
	// Root maps a top-level response name to the store key of the field on
	// ROOT_QUERY. This separates aliases/variables from normalized storage.
	Root map[string]string
	// Complete is false iff the last committed response for this query left
	// selected fields missing or skipped.
	Complete bool
}

// state is the immutable-on-published normalized cache content. Writes clone
// it copy-on-write and atomically publish the replacement.
type state struct {
	seq      int64
	entities map[string]*Entity
	queries  map[string]*QueryInfo
}

// RootQueryID is the fixed identity under which root fields are normalized.
const RootQueryID = "ROOT_QUERY"

// Snapshot is a read-only, immutable view of the cache at one sequence
// number. Readers always observe a fully committed snapshot.
type Snapshot struct {
	st *state
}
