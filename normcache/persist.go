package normcache

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
)

// 序列化格式版本，固定配置下保证可观测结果逐条一致。
const snapshotVersion = 1

// valueJSON 是记录字段值的可判别联合表示。
type valueJSON struct {
	Kind string               `json:"kind"` // scalar | ref | missing | errored | list | map
	Raw  json.RawMessage      `json:"raw,omitempty"`
	List []valueJSON          `json:"list,omitempty"`
	Map  map[string]valueJSON `json:"map,omitempty"`
}

func encodeValue(v any) valueJSON {
	switch t := v.(type) {
	case Reference:
		raw, _ := json.Marshal(t)
		return valueJSON{Kind: "ref", Raw: raw}
	case Missing:
		raw, _ := json.Marshal(t)
		return valueJSON{Kind: "missing", Raw: raw}
	case Errored:
		raw, _ := json.Marshal(t)
		return valueJSON{Kind: "errored", Raw: raw}
	case []any:
		list := make([]valueJSON, len(t))
		for i, e := range t {
			list[i] = encodeValue(e)
		}
		return valueJSON{Kind: "list", List: list}
	case map[string]any:
		m := make(map[string]valueJSON, len(t))
		for k, e := range t {
			m[k] = encodeValue(e)
		}
		return valueJSON{Kind: "map", Map: m}
	default:
		raw, _ := json.Marshal(t)
		return valueJSON{Kind: "scalar", Raw: raw}
	}
}

func decodeValue(v valueJSON) (any, error) {
	switch v.Kind {
	case "ref":
		var r Reference
		if err := json.Unmarshal(v.Raw, &r); err != nil {
			return nil, err
		}
		return r, nil
	case "missing":
		var m Missing
		if err := json.Unmarshal(v.Raw, &m); err != nil {
			return nil, err
		}
		return m, nil
	case "errored":
		var e Errored
		if err := json.Unmarshal(v.Raw, &e); err != nil {
			return nil, err
		}
		return e, nil
	case "list":
		out := make([]any, len(v.List))
		for i, e := range v.List {
			d, err := decodeValue(e)
			if err != nil {
				return nil, err
			}
			out[i] = d
		}
		return out, nil
	case "map":
		out := make(map[string]any, len(v.Map))
		for k, e := range v.Map {
			d, err := decodeValue(e)
			if err != nil {
				return nil, err
			}
			out[k] = d
		}
		return out, nil
	case "scalar":
		var s any
		if err := json.Unmarshal(v.Raw, &s); err != nil {
			return nil, err
		}
		return s, nil
	default:
		return nil, fmt.Errorf("unknown value kind %q", v.Kind)
	}
}

// recordJSON 是 Record 的序列化形式，字段按键排序。
type recordJSON struct {
	TypeName string               `json:"typeName,omitempty"`
	Fields   map[string]valueJSON `json:"fields,omitempty"`
}

// Snapshot 将缓存确定性地序列化到 w（持读锁，快照一致）。
func (c *Cache) Snapshot(w io.Writer) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	keys := make([]string, 0, len(c.records))
	for k := range c.records {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	snap := serializableSnapshot{Version: snapshotVersion, Options: c.opts, Records: make([]serializableEntry, 0, len(keys))}
	for _, k := range keys {
		snap.Records = append(snap.Records, serializableEntry{Key: k, Record: recordToJSON(c.records[k])})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(snap)
}

// serializableSnapshot 避免直接序列化内部 Record（字段值是 any）。
type serializableSnapshot struct {
	Version int                 `json:"version"`
	Options Options             `json:"options"`
	Records []serializableEntry `json:"records"`
}

type serializableEntry struct {
	Key    string     `json:"key"`
	Record recordJSON `json:"record"`
}

func recordToJSON(rec *Record) recordJSON {
	rj := recordJSON{TypeName: rec.TypeName}
	if len(rec.Fields) > 0 {
		rj.Fields = make(map[string]valueJSON, len(rec.Fields))
		for fk, fv := range rec.Fields {
			rj.Fields[fk] = encodeValue(fv)
		}
	}
	return rj
}

// Restore 从 r 加载缓存。加载后实体记录、字段与归一化引用关系与序列化前一致。
func Restore(r io.Reader) (*Cache, error) {
	var snap serializableSnapshot
	if err := json.NewDecoder(r).Decode(&snap); err != nil {
		return nil, fmt.Errorf("decode snapshot: %w", err)
	}
	if snap.Version != snapshotVersion {
		return nil, fmt.Errorf("unsupported snapshot version %d", snap.Version)
	}
	c := NewWithOptions(snap.Options)
	for _, e := range snap.Records {
		rec := &Record{TypeName: e.Record.TypeName, Fields: map[string]any{}}
		for fk, fv := range e.Record.Fields {
			v, err := decodeValue(fv)
			if err != nil {
				return nil, fmt.Errorf("record %s field %s: %w", e.Key, fk, err)
			}
			rec.Fields[fk] = v
		}
		c.records[e.Key] = rec
	}
	return c, nil
}
