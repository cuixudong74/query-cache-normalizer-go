package qcache

import (
	"context"
	"io"
	"os"
	"sort"
	"sync"
	"sync/atomic"
)

// Cache is a normalized query cache with snapshot isolation. A Read always
// observes one fully committed state; an in-flight Write is never visible,
// because writers mutate a private copy-on-write clone and only publish it
// atomically when complete.
type Cache struct {
	cfg Config
	mu  sync.RWMutex
	cur atomic.Pointer[state]
}

// New builds an empty cache.
func New(cfg Config) *Cache {
	c := &Cache{cfg: cfg}
	c.cur.Store(newState())
	return c
}

// WriteResult reports a committed write.
type WriteResult struct {
	Version int64
	// Complete is false when selected fields were missing or skipped.
	Complete bool
	// Skipped paths that were recorded as FieldSkipped.
	Skipped []string
}

// bind returns a deep-copied query with variable placeholders resolved in args.
func bind(q *Query) *Query {
	vars, _ := cloneValue(q.Variables).(map[string]any)
	bq := &Query{
		Name:       q.Name,
		Type:       q.Type,
		Variables:  vars,
		Selections: cloneSelections(q.Selections),
	}
	resolveSelectionArgs(bq.Selections, bq.Variables)
	return bq
}

// cloneSelections deep-copies a selection tree.
func cloneSelections(sels []Selection) []Selection {
	out := make([]Selection, len(sels))
	for i, s := range sels {
		cp := s
		cp.Args = cloneValue(s.Args).(map[string]any)
		if len(s.Selections) > 0 {
			cp.Selections = cloneSelections(s.Selections)
		}
		out[i] = cp
	}
	return out
}

func resolveSelectionArgs(sels []Selection, vars map[string]any) {
	for i := range sels {
		sels[i].Args = resolveArgs(sels[i].Args, vars)
		if len(sels[i].Selections) > 0 {
			resolveSelectionArgs(sels[i].Selections, vars)
		}
	}
}

// Register records a query's shape without writing data and returns its
// fingerprint. Re-registering the same fingerprint is a no-op.
func (c *Cache) Register(q *Query) (string, error) {
	if q == nil {
		return "", ErrInvalidResponse
	}
	if q.Type == "" {
		q.Type = OpQuery
	}
	bq := bind(q)
	fp := queryFingerprint(bq)

	c.mu.Lock()
	defer c.mu.Unlock()
	st := c.cur.Load()
	if _, ok := st.queries[fp]; !ok {
		next := st.clone()
		next.queries[fp] = &QueryInfo{Query: bq, Root: map[string]string{}}
		c.publish(next)
	}
	return fp, nil
}

// Read rebuilds a previously registered query's result from the current
// snapshot, with no locks held during traversal.
func (c *Cache) Read(fingerprint string) (ReadResult, error) {
	st := c.cur.Load()
	qi, ok := st.queries[fingerprint]
	if !ok {
		return ReadResult{}, ErrNotFound
	}
	return rebuild(st, qi), nil
}

// ReadQuery registers q (if new) and rebuilds it.
func (c *Cache) ReadQuery(q *Query) (ReadResult, error) {
	fp, err := c.Register(q)
	if err != nil {
		return ReadResult{}, err
	}
	return c.Read(fp)
}

// Write normalizes and commits one response for q. An error response with no
// data commits nothing and leaves the prior snapshot intact. A response with
// data plus field-level errors commits only the healthy fields and marks the
// errored ones skipped.
func (c *Cache) Write(q *Query, resp *Response) (WriteResult, error) {
	if q == nil || resp == nil {
		return WriteResult{}, ErrInvalidResponse
	}
	if resp.Data == nil && len(resp.Errors) > 0 {
		// Top-level failure: never pollute the existing cache.
		return WriteResult{Version: c.cur.Load().seq}, ErrTopLevelFailure
	}
	if resp.Data == nil {
		return WriteResult{}, ErrInvalidResponse
	}
	bq := bind(q)
	fp := queryFingerprint(bq)

	c.mu.Lock()
	defer c.mu.Unlock()

	st := c.cur.Load()
	next := st.clone()
	qi, ok := next.queries[fp]
	if !ok {
		qi = &QueryInfo{Query: bq, Root: map[string]string{}}
		next.queries[fp] = qi
	} else {
		qi.Query = bq // refresh bound document (resolved variables)
	}

	errIndex := indexByPath(resp.Errors)
	complete := normalize(c.cfg, next, qi, resp, errIndex)
	qi.Complete = complete

	res := rebuild(next, qi)
	c.publish(next)

	wr := WriteResult{Version: next.seq, Complete: complete, Skipped: append([]string{}, res.Skipped...)}
	sort.Strings(wr.Skipped)
	return wr, nil
}

// publish bumps the sequence and atomically installs the new snapshot.
func (c *Cache) publish(next *state) {
	next.seq++
	c.cur.Store(next)
}

// Fetch executes q against src and then writes the response. A transport error
// or top-level failure leaves the cache unchanged.
func (c *Cache) Fetch(ctx context.Context, src Source, q *Query) (*Response, WriteResult, error) {
	resp, err := src.Execute(ctx, q)
	if err != nil {
		return nil, WriteResult{Version: c.cur.Load().seq}, err
	}
	wr, err := c.Write(q, resp)
	return resp, wr, err
}

// InvalidateEntity drops an entity and everything referencing it.
func (c *Cache) InvalidateEntity(id string) (InvalidateResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := c.cur.Load()
	next := st.clone()
	missing := invalidateEntity(next, id)
	if len(missing) == 0 {
		return InvalidateResult{Version: next.seq}, nil
	}
	affected := affectedQueries(next, missing)
	invalids := sortedKeys(missing)
	c.publish(next)
	return InvalidateResult{
		Version:           next.seq,
		InvalidatedFields: invalids,
		AffectedQueries:   affected,
	}, nil
}

// InvalidateField drops one normalized field "id.field".
func (c *Cache) InvalidateField(id, field string) (InvalidateResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := c.cur.Load()
	next := st.clone()
	missing := invalidateField(next, id, field)
	if len(missing) == 0 {
		return InvalidateResult{Version: next.seq}, nil
	}
	affected := affectedQueries(next, missing)
	c.publish(next)
	return InvalidateResult{
		Version:           next.seq,
		InvalidatedFields: sortedKeys(missing),
		AffectedQueries:   affected,
	}, nil
}

// GC reclaims data unreachable from any registered query.
func (c *Cache) GC() (InvalidateResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := c.cur.Load()
	next := st.clone()
	removed := gc(next)
	c.publish(next)
	return InvalidateResult{Version: next.seq, RemovedEntities: removed}, nil
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Snapshot returns the current immutable snapshot.
func (c *Cache) Snapshot() (*Snapshot, int64) {
	st := c.cur.Load()
	return &Snapshot{st: st}, st.seq
}

// EntityCount reports the number of normalized entities (including ROOT_QUERY).
func (s *Snapshot) EntityCount() int { return len(s.st.entities) }

// Version reports the snapshot's commit sequence.
func (s *Snapshot) Version() int64 { return s.st.seq }

// Entities returns a stable, sorted summary of stored entity keys.
func (s *Snapshot) Entities() []string {
	out := make([]string, 0, len(s.st.entities))
	for k := range s.st.entities {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// HasEntity reports whether an entity id is present in the snapshot.
func (s *Snapshot) HasEntity(id string) bool {
	_, ok := s.st.entities[id]
	return ok
}

// Save persists the current snapshot using the configured PersistPath.
func (c *Cache) Save() error {
	if c.cfg.PersistPath == "" {
		return ErrInvalidResponse
	}
	f, err := os.Create(c.cfg.PersistPath)
	if err != nil {
		return err
	}
	defer f.Close()
	return c.SaveTo(f)
}

// Load replaces cache content from the configured PersistPath.
func (c *Cache) Load() error {
	if c.cfg.PersistPath == "" {
		return ErrInvalidResponse
	}
	f, err := os.Open(c.cfg.PersistPath)
	if err != nil {
		return err
	}
	defer f.Close()
	return c.LoadFrom(f)
}

// SaveTo persists to a writer.
func (c *Cache) SaveTo(w io.Writer) error {
	c.mu.RLock()
	st := c.cur.Load()
	c.mu.RUnlock()
	return writeJSONTo(w, encodeState(st))
}

// LoadFrom replaces content from a reader.
func (c *Cache) LoadFrom(r io.Reader) error {
	d, err := readJSONFrom(r)
	if err != nil {
		return err
	}
	st, err := decodeState(d)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cur.Store(st)
	return nil
}
