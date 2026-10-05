package normcache

import (
	"bytes"
	"testing"
)

// 自定义身份字段：实体按配置的字段归一化。
func TestCustomIDField(t *testing.T) {
	c := NewWithOptions(Options{IDField: "key", TypenameField: "kind"})
	q := mustParse(t, `{ node { kind key label } }`)
	c.Write(q, nil, map[string]any{
		"node": map[string]any{"kind": "Widget", "key": "w1", "label": "A"},
	}, nil)
	if _, ok := c.records["Widget:w"]; ok {
		t.Fatal("should not use default keying")
	}
	if _, ok := c.records["Widget:w1"]; !ok {
		t.Fatalf("expected Widget:w1 record, got %v", c.records)
	}
	res := c.Read(q, nil)
	if !res.Complete {
		t.Fatalf("incomplete: %+v", res.Data)
	}
	node := res.Data["node"].(map[string]any)
	if node["kind"] != "Widget" {
		t.Fatalf("typename field should round-trip, got %+v", node)
	}

	// 配置随快照持久化
	var buf bytes.Buffer
	if err := c.Snapshot(&buf); err != nil {
		t.Fatal(err)
	}
	restored, err := Restore(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if restored.opts.IDField != "key" || restored.opts.TypenameField != "kind" {
		t.Fatalf("options not persisted: %+v", restored.opts)
	}
	if r := restored.Read(q, nil); !r.Complete {
		t.Fatalf("restored read incomplete: %+v", r.Data)
	}
}
