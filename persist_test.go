package qcache

import (
	"bytes"
	"path/filepath"
	"reflect"
	"testing"
)

func seedCache(t *testing.T) *Cache {
	t.Helper()
	c := New(defaultConfig())
	q := &Query{Name: "Me", Type: OpQuery, Selections: []Selection{
		{Name: "viewer", Selections: []Selection{
			{Name: "id"}, {Name: "name"},
			{Name: "friends", Selections: []Selection{{Name: "id"}, {Name: "name"}}},
		}},
	}}
	resp := &Response{
		Data: map[string]any{
			"viewer": map[string]any{
				"__typename": "User", "id": int64(9), "name": "Ada",
				"friends": []any{
					map[string]any{"__typename": "User", "id": int64(2), "name": "Lin"},
				},
			},
		},
		Errors: []FieldError{{Message: "partial", Path: []any{"viewer", "x"}}},
	}
	// Use a query selecting x to record a skipped field meaningfully.
	q2 := &Query{Name: "Me", Type: OpQuery, Selections: []Selection{
		{Name: "viewer", Selections: []Selection{
			{Name: "id"}, {Name: "name"}, {Name: "x"},
			{Name: "friends", Selections: []Selection{{Name: "id"}, {Name: "name"}}},
		}},
	}}
	resp2 := &Response{
		Data: map[string]any{
			"viewer": map[string]any{
				"__typename": "User", "id": int64(9), "name": "Ada",
				"friends": []any{
					map[string]any{"__typename": "User", "id": int64(2), "name": "Lin"},
				},
			},
		},
		Errors: []FieldError{{Message: "partial", Path: []any{"viewer", "x"}}},
	}
	if _, err := c.Write(q2, resp2); err != nil {
		t.Fatal(err)
	}
	_ = q
	_ = resp
	return c
}

func TestPersistRoundTripPreservesNormalization(t *testing.T) {
	c1 := seedCache(t)
	var buf bytes.Buffer
	if err := c1.SaveTo(&buf); err != nil {
		t.Fatal(err)
	}

	c2 := New(defaultConfig())
	if err := c2.LoadFrom(&buf); err != nil {
		t.Fatal(err)
	}

	s1, v1 := c1.Snapshot()
	s2, v2 := c2.Snapshot()
	if v1 != v2 {
		t.Fatalf("version %d != %d", v1, v2)
	}
	if !reflect.DeepEqual(s1.Entities(), s2.Entities()) {
		t.Fatalf("entities differ: %v vs %v", s1.Entities(), s2.Entities())
	}

	// Rebuild identical results after reload, including the skipped field.
	q := &Query{Name: "Me", Type: OpQuery, Selections: []Selection{
		{Name: "viewer", Selections: []Selection{
			{Name: "id"}, {Name: "name"}, {Name: "x"},
			{Name: "friends", Selections: []Selection{{Name: "id"}, {Name: "name"}}},
		}},
	}}
	r1, _ := c1.ReadQuery(q)
	r2, _ := c2.ReadQuery(q)
	if !reflect.DeepEqual(canonicalJSON(r1.Data), canonicalJSON(r2.Data)) {
		t.Fatalf("rebuilt differs after reload:\n%v\n%v", canonicalJSON(r1.Data), canonicalJSON(r2.Data))
	}
	if !reflect.DeepEqual(r1.Skipped, r2.Skipped) {
		t.Fatalf("skipped differs: %v vs %v", r1.Skipped, r2.Skipped)
	}
	v := r2.Data["viewer"].(map[string]any)["x"]
	if _, ok := v.(SkippedMarker); !ok {
		t.Fatalf("skipped marker lost: %T", v)
	}
}

func TestPersistDeterministic(t *testing.T) {
	a := canonicalJSON(seedState(t))
	b := canonicalJSON(seedState(t))
	if a != b {
		t.Fatalf("serialization not deterministic\n%s\n%s", a, b)
	}
}

func seedState(t *testing.T) any {
	c := seedCache(t)
	return dtoToAny(encodeState(c.cur.Load()))
}

func TestFileSaveLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")
	c1 := New(Config{IDFields: map[string][]string{"": {"id"}}, PersistPath: path})
	q := userQ("U", "", int64(1), "id", "name")
	if _, err := c1.Write(q, &Response{Data: map[string]any{
		"user": map[string]any{"__typename": "User", "id": int64(1), "name": "Ada"},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := c1.Save(); err != nil {
		t.Fatal(err)
	}
	c2 := New(Config{IDFields: map[string][]string{"": {"id"}}, PersistPath: path})
	if err := c2.Load(); err != nil {
		t.Fatal(err)
	}
	r, err := c2.Read("U|{}")
	if err != nil {
		t.Fatal(err)
	}
	if r.Data["user"].(map[string]any)["name"] != "Ada" {
		t.Fatalf("reload read = %v", r.Data)
	}
}

func TestOperationSequenceDeterministic(t *testing.T) {
	// Same fixed operation sequence on two independent caches must produce
	// byte-identical observable output.
	run := func() string {
		c := New(defaultConfig())
		var out bytes.Buffer
		q := userQ("U", "", int64(1), "id", "name")
		_, _ = c.Write(q, &Response{Data: map[string]any{
			"user": map[string]any{"__typename": "User", "id": int64(1), "name": "Ada"},
		}})
		_, _ = c.Write(q, &Response{Data: map[string]any{
			"user": map[string]any{"__typename": "User", "id": int64(1), "name": "Ada2"},
		}})
		r, _ := c.ReadQuery(q)
		out.WriteString(canonicalJSON(r.Data))
		_ = c.SaveTo(&out)
		return out.String()
	}
	if run() != run() {
		t.Fatal("observable output not deterministic across identical sequences")
	}
}
