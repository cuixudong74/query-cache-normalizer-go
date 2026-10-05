// Package qcache implements a normalized, snapshot-isolated cache for query
// clients (GraphQL-like documents over an in-memory or file-backed simulated
// data source).
package qcache

// OperationType identifies the kind of operation in a Query.
type OperationType string

const (
	// OpQuery is the only supported operation type.
	OpQuery OperationType = "query"
)

// Selection is one field in a query document. Nested objects and lists carry
// their own Selections. TypeName is an inline-fragment type condition used for
// union/interface fields; empty means the selection applies unconditionally.
type Selection struct {
	Name       string         `json:"name"`
	Alias      string         `json:"alias,omitempty"`
	Args       map[string]any `json:"args,omitempty"`
	TypeName   string         `json:"typeName,omitempty"`
	Selections []Selection    `json:"selections,omitempty"`
}

// ResponseName returns the key under which the field appears in response data
// (Alias when present, otherwise Name).
func (s Selection) ResponseName() string {
	if s.Alias != "" {
		return s.Alias
	}
	return s.Name
}

// Query is a bound query document. Args may contain Variable placeholders; use
// ResolveArgs/Bind to substitute Variables before talking to the cache.
type Query struct {
	Name       string         `json:"name,omitempty"`
	Type       OperationType  `json:"type"`
	Variables  map[string]any `json:"variables,omitempty"`
	Selections []Selection    `json:"selections"`
}

// FieldError is a partial-failure error located at a response path. Path
// elements are response keys (strings, aliases honored) or list indices (ints).
type FieldError struct {
	Message string `json:"message"`
	Path    []any  `json:"path,omitempty"`
}

// Response is a server response: Data plus zero or more located FieldErrors.
// A nil Data with errors is a top-level failure and writes nothing.
type Response struct {
	Data   map[string]any `json:"data"`
	Errors []FieldError   `json:"errors,omitempty"`
}

// MarkerKind enumerates the distinguishable non-value markers a read can emit.
type MarkerKind string

const (
	// MarkMissing means the field was never cached (absent, not null).
	MarkMissing MarkerKind = "missing"
	// MarkSkipped means the field was skipped because the server returned an
	// error for that path without a usable value.
	MarkSkipped MarkerKind = "skipped"
)

// Marker is a distinguishable placeholder in rebuilt read data. It never
// masquerades as null or a zero value.
type Marker interface {
	MarkerKind() MarkerKind
}

// MissingMarker marks a field that is absent from the cache.
type MissingMarker struct {
	// Reason is a fixed, non-data diagnostic string.
	Reason string `json:"reason"`
}

// MarkerKind implements Marker.
func (MissingMarker) MarkerKind() MarkerKind { return MarkMissing }

// SkippedMarker marks a field whose server value was skipped due to an error.
type SkippedMarker struct {
	// Error is the FieldError that caused the skip.
	Error FieldError `json:"error"`
}

// MarkerKind implements Marker.
func (SkippedMarker) MarkerKind() MarkerKind { return MarkSkipped }
