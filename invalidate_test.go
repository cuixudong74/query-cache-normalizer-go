package qcache

import "testing"

func TestInvalidateEntityAndReadConsistency(t *testing.T) {
	c := New(defaultConfig())
	q := &Query{Name: "U", Type: OpQuery, Selections: []Selection{
		{Name: "user", Args: map[string]any{"id": int64(1)}, Selections: []Selection{
			{Name: "id"}, {Name: "name"},
			{Name: "friend", Selections: []Selection{{Name: "id"}, {Name: "name"}}},
		}},
	}}
	if _, err := c.Write(q, &Response{Data: map[string]any{
		"user": map[string]any{
			"__typename": "User", "id": int64(1), "name": "Ada",
			"friend": map[string]any{"__typename": "User", "id": int64(2), "name": "Lin"},
		},
	}}); err != nil {
		t.Fatal(err)
	}

	res, err := c.InvalidateEntity("User:2")
	if err != nil {
		t.Fatal(err)
	}
	if snap, _ := c.Snapshot(); snap.HasEntity("User:2") {
		t.Fatal("invalidated entity still present")
	}
	found := false
	for _, a := range res.AffectedQueries {
		if a == "U|{}" {
			found = true
		}
	}
	if !found {
		t.Fatalf("query referencing invalidated entity not reported: %v", res.AffectedQueries)
	}

	// Re-read behaves like a refetch against an empty cache for that field:
	// friend is a distinguishable hole, user itself is intact.
	r, _ := c.ReadQuery(q)
	u := r.Data["user"].(map[string]any)
	if u["name"] != "Ada" {
		t.Fatalf("unrelated field changed: %v", u["name"])
	}
	switch u["friend"].(type) {
	case MissingMarker:
	default:
		t.Fatalf("friend = %T, want MissingMarker", u["friend"])
	}
}

func TestInvalidateFieldTargeted(t *testing.T) {
	c := New(defaultConfig())
	q := userQ("U", "", int64(1), "id", "name", "email")
	if _, err := c.Write(q, &Response{Data: map[string]any{
		"user": map[string]any{"__typename": "User", "id": int64(1), "name": "Ada", "email": "a@x"},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.InvalidateField("User:1", "email"); err != nil {
		t.Fatal(err)
	}
	r, _ := c.ReadQuery(q)
	u := r.Data["user"].(map[string]any)
	if u["name"] != "Ada" {
		t.Fatalf("name lost on field invalidation: %v", u["name"])
	}
	switch u["email"].(type) {
	case MissingMarker:
	default:
		t.Fatalf("email = %T, want MissingMarker", u["email"])
	}
}

func TestGCReclaimsUnreferencedData(t *testing.T) {
	c := New(defaultConfig())
	q1 := userQ("Q1", "", int64(1), "id", "name")
	if _, err := c.Write(q1, &Response{Data: map[string]any{
		"user": map[string]any{"__typename": "User", "id": int64(1), "name": "Ada"},
	}}); err != nil {
		t.Fatal(err)
	}
	q2 := userQ("Q2", "", int64(2), "id", "name")
	if _, err := c.Write(q2, &Response{Data: map[string]any{
		"user": map[string]any{"__typename": "User", "id": int64(2), "name": "Lin"},
	}}); err != nil {
		t.Fatal(err)
	}
	// Drop the second query's registration by reloading minimal registrations:
	// simulate by invalidating its root field, then GC should reclaim User:2.
	if _, err := c.InvalidateField(RootQueryID, `user({"id":2})`); err != nil {
		t.Fatal(err)
	}
	res, err := c.GC()
	if err != nil {
		t.Fatal(err)
	}
	if snap, _ := c.Snapshot(); snap.HasEntity("User:2") {
		t.Fatalf("User:2 not reclaimed; removed=%v", res.RemovedEntities)
	}
	if snap, _ := c.Snapshot(); !snap.HasEntity("User:1") {
		t.Fatal("User:1 wrongly reclaimed while still referenced")
	}
}
