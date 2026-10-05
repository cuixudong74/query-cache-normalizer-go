package qcache

import "testing"

func TestNestedEntityErrorSkipped(t *testing.T) {
	c := New(defaultConfig())
	q := &Query{Name: "V", Type: OpQuery, Selections: []Selection{
		{Name: "viewer", Selections: []Selection{
			{Name: "id"},
			{Name: "company", Selections: []Selection{{Name: "id"}, {Name: "name"}, {Name: "ceo"}}},
		}},
	}}
	resp := &Response{
		Data: map[string]any{"viewer": map[string]any{
			"__typename": "User", "id": int64(9),
			"company": map[string]any{"__typename": "Org", "id": int64(3), "name": "ACME"},
		}},
		Errors: []FieldError{{Message: "ceo blocked", Path: []any{"viewer", "company", "ceo"}}},
	}
	if _, err := c.Write(q, resp); err != nil {
		t.Fatal(err)
	}
	r, _ := c.ReadQuery(q)
	company := r.Data["viewer"].(map[string]any)["company"].(map[string]any)
	if company["name"] != "ACME" {
		t.Fatalf("company name = %v", company["name"])
	}
	if _, ok := company["ceo"].(SkippedMarker); !ok {
		t.Fatalf("ceo = %T, want SkippedMarker", company["ceo"])
	}
}

func TestAbsentResponseFieldLeavesCache(t *testing.T) {
	c := New(defaultConfig())
	qFull := &Query{Name: "V", Type: OpQuery, Selections: []Selection{
		{Name: "viewer", Selections: []Selection{{Name: "id"}, {Name: "name"}, {Name: "email"}}},
	}}
	if _, err := c.Write(qFull, &Response{Data: map[string]any{"viewer": map[string]any{
		"__typename": "User", "id": int64(9), "name": "Ada", "email": "a@x",
	}}}); err != nil {
		t.Fatal(err)
	}
	// A different query (distinct fingerprint) selects a thinner shape and
	// omits email. It must not erase email from the merged normalized record.
	qThin := &Query{Name: "VThin", Type: OpQuery, Selections: []Selection{
		{Name: "viewer", Selections: []Selection{{Name: "id"}, {Name: "name"}}},
	}}
	if _, err := c.Write(qThin, &Response{Data: map[string]any{"viewer": map[string]any{
		"__typename": "User", "id": int64(9), "name": "Ada2",
	}}}); err != nil {
		t.Fatal(err)
	}
	r, _ := c.ReadQuery(qFull)
	v := r.Data["viewer"].(map[string]any)
	if v["name"] != "Ada2" {
		t.Fatalf("name = %v", v["name"])
	}
	if v["email"] != "a@x" {
		t.Fatalf("omitted field erased/changed: %v", v["email"])
	}
	if !r.Complete {
		t.Fatal("omitted-but-cached field must keep query complete")
	}
}

func TestGCKeepsReachableNesting(t *testing.T) {
	c := New(defaultConfig())
	q := &Query{Name: "V", Type: OpQuery, Selections: []Selection{
		{Name: "viewer", Selections: []Selection{
			{Name: "id"}, {Name: "name"},
			{Name: "company", Selections: []Selection{{Name: "id"}, {Name: "name"}}},
		}},
	}}
	if _, err := c.Write(q, &Response{Data: map[string]any{"viewer": map[string]any{
		"__typename": "User", "id": 9, "name": "Ada",
		"company": map[string]any{"__typename": "Org", "id": 3, "name": "ACME"},
	}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GC(); err != nil {
		t.Fatal(err)
	}
	snap, _ := c.Snapshot()
	if !snap.HasEntity("Org:3") || !snap.HasEntity("User:9") {
		t.Fatalf("reachable nested entity reclaimed: %v", snap.Entities())
	}
}
