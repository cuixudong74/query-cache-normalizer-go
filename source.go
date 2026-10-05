package qcache

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// Source simulates a query backend without external services. Implementations
// serve pre-canned Responses keyed by the bound query's fingerprint.
type Source interface {
	Execute(ctx context.Context, q *Query) (*Response, error)
}

// MemorySource serves responses from an in-memory table.
type MemorySource struct {
	mu        sync.RWMutex
	responses map[string]*Response
}

// NewMemorySource builds an empty MemorySource.
func NewMemorySource() *MemorySource {
	return &MemorySource{responses: map[string]*Response{}}
}

// Set registers a response for a query name + variables pair.
func (m *MemorySource) Set(q *Query, resp *Response) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.responses[queryFingerprint(q)] = resp
}

// Execute implements Source.
func (m *MemorySource) Execute(ctx context.Context, q *Query) (*Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	resp, ok := m.responses[queryFingerprint(q)]
	if !ok {
		return nil, fmt.Errorf("qcache: no response fixture for %q", queryFingerprint(q))
	}
	return resp, nil
}

// fixtureFile is the on-disk shape used by FileSource:
//
//	{"responses": {"<fingerprint>": {"data": {...}, "errors": [...]}}}
type fixtureFile struct {
	Responses map[string]*Response `json:"responses"`
}

// FileSource loads JSON-serialized response fixtures from a file, simulating a
// file-backed backend. The file is re-read on each Execute so external edits
// are visible, without contacting any service.
type FileSource struct {
	path string
}

// NewFileSource loads and validates a fixture file.
func NewFileSource(path string) (*FileSource, error) {
	var fx fixtureFile
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("qcache: read source file: %w", err)
	}
	if err := json.Unmarshal(raw, &fx); err != nil {
		return nil, fmt.Errorf("qcache: parse source file: %w", err)
	}
	return &FileSource{path: path}, nil
}

// Execute implements Source.
func (f *FileSource) Execute(ctx context.Context, q *Query) (*Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var fx fixtureFile
	raw, err := os.ReadFile(f.path)
	if err != nil {
		return nil, fmt.Errorf("qcache: read source file: %w", err)
	}
	if err := json.Unmarshal(raw, &fx); err != nil {
		return nil, fmt.Errorf("qcache: parse source file: %w", err)
	}
	resp, ok := fx.Responses[queryFingerprint(q)]
	if !ok {
		return nil, fmt.Errorf("qcache: no response fixture for %q", queryFingerprint(q))
	}
	return resp, nil
}
