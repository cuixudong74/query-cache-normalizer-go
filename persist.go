package qcache

import (
	"bytes"
	"encoding/json"
	"io"
)

// snapshotDTO is the deterministic serialization form of a state.
type snapshotDTO struct {
	Seq      int64                   `json:"seq"`
	Entities map[string]entityDTO    `json:"entities"`
	Queries  map[string]queryInfoDTO `json:"queries"`
}

type entityDTO struct {
	Typename string              `json:"__typename,omitempty"`
	Fields   map[string]fieldDTO `json:"fields"`
}

type fieldDTO struct {
	Status FieldStatus `json:"status"`
	Value  any         `json:"value,omitempty"`
	Err    *FieldError `json:"err,omitempty"`
}

type queryInfoDTO struct {
	Query    *Query            `json:"query"`
	Root     map[string]string `json:"root"`
	Complete bool              `json:"complete"`
}

// refTag marks normalized references in persisted JSON.
const refTag = "$ref"

// skipTag marks embedded skipped fields in persisted JSON.
const skipTag = "$skipped"

func encodeState(st *state) *snapshotDTO {
	d := &snapshotDTO{
		Seq:      st.seq,
		Entities: make(map[string]entityDTO, len(st.entities)),
		Queries:  make(map[string]queryInfoDTO, len(st.queries)),
	}
	for id, e := range st.entities {
		ed := entityDTO{Typename: e.Typename, Fields: map[string]fieldDTO{}}
		for name, f := range e.Fields {
			fd := fieldDTO{Status: f.Status, Err: f.Err}
			if f.Status == FieldPresent {
				fd.Value = encodeValue(f.Value)
			}
			ed.Fields[name] = fd
		}
		d.Entities[id] = ed
	}
	for fp, qi := range st.queries {
		d.Queries[fp] = queryInfoDTO{Query: qi.Query, Root: qi.Root, Complete: qi.Complete}
	}
	return d
}

func decodeState(d *snapshotDTO) (*state, error) {
	st := newState()
	st.seq = d.Seq
	for id, ed := range d.Entities {
		e := &Entity{Typename: ed.Typename, Fields: map[string]Field{}}
		for name, fd := range ed.Fields {
			f := Field{Status: fd.Status, Err: fd.Err}
			if fd.Status == FieldPresent {
				v, err := decodeValue(fd.Value)
				if err != nil {
					return nil, err
				}
				f.Value = v
			}
			e.Fields[name] = f
		}
		st.entities[id] = e
	}
	for fp, qd := range d.Queries {
		st.queries[fp] = &QueryInfo{Query: qd.Query, Root: qd.Root, Complete: qd.Complete}
	}
	return st, nil
}

// encodeValue renders normalized values for JSON, marking Refs and embedded
// skipped sentinels so they round-trip.
func encodeValue(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case Ref:
		return map[string]any{refTag: t.ID}
	case embeddedSkipped:
		return map[string]any{skipTag: t.Err}
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, e := range t {
			m[k] = encodeValue(e)
		}
		return m
	case []any:
		s := make([]any, len(t))
		for i, e := range t {
			s[i] = encodeValue(e)
		}
		return s
	default:
		return v
	}
}

func decodeValue(v any) (any, error) {
	switch t := v.(type) {
	case nil:
		return nil, nil
	case map[string]any:
		if id, ok := t[refTag]; ok {
			s, _ := id.(string)
			return Ref{ID: s}, nil
		}
		if e, ok := t[skipTag]; ok {
			fe, err := decodeFieldError(e)
			if err != nil {
				return nil, err
			}
			return embeddedSkipped{Err: fe}, nil
		}
		m := make(map[string]any, len(t))
		for k, e := range t {
			dv, err := decodeValue(e)
			if err != nil {
				return nil, err
			}
			m[k] = dv
		}
		return m, nil
	case []any:
		s := make([]any, len(t))
		for i, e := range t {
			dv, err := decodeValue(e)
			if err != nil {
				return nil, err
			}
			s[i] = dv
		}
		return s, nil
	default:
		return v, nil
	}
}

// decodeFieldError recovers a FieldError that json.Unmarshal produced as a
// generic map; Path numbers arrive as float64.
func decodeFieldError(v any) (FieldError, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return FieldError{}, nil
	}
	fe := FieldError{}
	if msg, ok := m["message"].(string); ok {
		fe.Message = msg
	}
	if p, ok := m["path"].([]any); ok {
		fe.Path = make([]any, len(p))
		for i, e := range p {
			fe.Path[i] = e
		}
	}
	return fe, nil
}

// writeJSONTo writes the snapshot with sorted object keys for determinism.
func writeJSONTo(w io.Writer, d *snapshotDTO) error {
	var buf bytes.Buffer
	writeCanonical(&buf, dtoToAny(d))
	buf.WriteByte('\n')
	_, err := w.Write(buf.Bytes())
	return err
}

// dtoToAny converts the typed DTO to generic values so writeCanonical (which
// works on maps/slices) governs key ordering. Numbers stay as json.Number via a
// typed re-marshal below; here we just need ordering, so marshal then unmarshal.
func dtoToAny(d *snapshotDTO) any {
	raw, err := json.Marshal(d)
	if err != nil {
		return nil
	}
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil
	}
	return v
}

// readJSONFrom parses a snapshot using json.Number so integers do not silently
// become float64; numbers are normalized to int64/float64 afterward.
func readJSONFrom(r io.Reader) (*snapshotDTO, error) {
	var generic any
	dec := json.NewDecoder(r)
	dec.UseNumber()
	if err := dec.Decode(&generic); err != nil {
		return nil, err
	}
	generic = normalizeNumbers(generic)
	raw, err := json.Marshal(generic)
	if err != nil {
		return nil, err
	}
	var d snapshotDTO
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// normalizeNumbers turns json.Number into int64 when integral, else float64.
func normalizeNumbers(v any) any {
	switch t := v.(type) {
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return i
		}
		if f, err := t.Float64(); err == nil {
			return f
		}
		return t.String()
	case map[string]any:
		for k, e := range t {
			t[k] = normalizeNumbers(e)
		}
		return t
	case []any:
		for i, e := range t {
			t[i] = normalizeNumbers(e)
		}
		return t
	default:
		return v
	}
}
