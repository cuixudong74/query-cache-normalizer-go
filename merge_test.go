package qcache

import (
	"testing"
)

func userQ(name, alias string, id any, fields ...string) *Query {
	sels := make([]Selection, 0, len(fields))
	for _, f := range fields {
		sels = append(sels, Selection{Name: f})
	}
	return &Query{
		Name: name,
		Type: OpQuery,
		Selections: []Selection{
			{Name: "user", Alias: alias, Args: map[string]any{"id": id}, Selections: sels},
		},
	}
}

func TestEntityMergesAcrossQueryAliasAndVariables(t *testing.T) {
	c := New(defaultConfig())

	qA := userQ("A", "", Variable{Name: "uid"}, "id", "name")
	qA.Variables = map[string]any{"uid": 7}
	if _, err := c.Write(qA, &Response{Data: map[string]any{
		"user": map[string]any{"__typename": "User", "id": int64(7), "name": "Ada"},
	}}); err != nil {
		t.Fatal(err)
	}

	qB := userQ("B", "who", int64(7), "id", "email")
	if _, err := c.Write(qB, &Response{Data: map[string]any{
		"who": map[string]any{"__typename": "User", "id": int64(7), "email": "ada@x"},
	}}); err != nil {
		t.Fatal(err)
	}

	snap, _ := c.Snapshot()
	if n := snap.EntityCount(); n != 2 { // ROOT_QUERY + User:7
		t.Fatalf("expected single merged entity, got %d: %v", n, snap.Entities())
	}
	e, ok := snap.st.entities["User:7"]
	if !ok {
		t.Fatal("missing User:7")
	}
	if got := e.Fields["name"].Value; got != "Ada" {
		t.Fatalf("name = %v", got)
	}
	if got := e.Fields["email"].Value; got != "ada@x" {
		t.Fatalf("email = %v", got)
	}
	// After variable resolution both queries target user(id:7); the alias
	// must not split storage, so they share one root field key.
	root := snap.st.entities[RootQueryID]
	if len(root.Fields) != 1 {
		t.Fatalf("root fields = %d, want 1 shared key", len(root.Fields))
	}
}

func TestConflictLastPresentWriteWins(t *testing.T) {
	c := New(defaultConfig())
	q := userQ("A", "", int64(1), "id", "name")
	write := func(name string) {
		t.Helper()
		if _, err := c.Write(q, &Response{Data: map[string]any{
			"user": map[string]any{"__typename": "User", "id": int64(1), "name": name},
		}}); err != nil {
			t.Fatal(err)
		}
	}
	write("Ada")
	write("Augusta")
	r, err := c.ReadQuery(q)
	if err != nil {
		t.Fatal(err)
	}
	u := r.Data["user"].(map[string]any)
	if u["name"] != "Augusta" {
		t.Fatalf("conflict resolution = %v, want newest write", u["name"])
	}
}

func TestExplicitNullIsNotMissing(t *testing.T) {
	c := New(defaultConfig())
	q := userQ("A", "", int64(1), "id", "name", "email")
	if _, err := c.Write(q, &Response{Data: map[string]any{
		"user": map[string]any{"__typename": "User", "id": int64(1), "name": nil, "email": "a@x"},
	}}); err != nil {
		t.Fatal(err)
	}
	r, _ := c.ReadQuery(q)
	u := r.Data["user"].(map[string]any)
	v, present := u["name"]
	if !present {
		t.Fatal("explicit null field must be present")
	}
	if v != nil {
		t.Fatalf("explicit null = %v, want nil", v)
	}
	if !r.Complete {
		t.Fatal("explicit null must not mark query incomplete")
	}
}

func TestMergeAbsentFieldsDoNotOverwrite(t *testing.T) {
	c := New(defaultConfig())
	q1 := userQ("A", "", int64(1), "id", "name")
	if _, err := c.Write(q1, &Response{Data: map[string]any{
		"user": map[string]any{"__typename": "User", "id": int64(1), "name": "Ada"},
	}}); err != nil {
		t.Fatal(err)
	}
	// Second shape selects email only; name is simply not returned and must
	// survive the merge rather than be erased.
	q2 := userQ("B", "", int64(1), "id", "name", "email")
	if _, err := c.Write(q2, &Response{Data: map[string]any{
		"user": map[string]any{"__typename": "User", "id": int64(1), "email": "a@x"},
	}}); err != nil {
		t.Fatal(err)
	}
	r, _ := c.ReadQuery(q2)
	u := r.Data["user"].(map[string]any)
	if u["name"] != "Ada" || u["email"] != "a@x" {
		t.Fatalf("merged fields wrong: %v", u)
	}
}
