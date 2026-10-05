package qcache

import "errors"

// ErrTopLevelFailure is returned by Write when the response has no data and
// only errors; nothing is written.
var ErrTopLevelFailure = errors.New("qcache: top-level response failure, nothing written")

// ErrInvalidResponse is returned when a response cannot be normalized.
var ErrInvalidResponse = errors.New("qcache: invalid response")

// ErrNotFound is returned by lookups for unknown keys.
var ErrNotFound = errors.New("qcache: not found")
