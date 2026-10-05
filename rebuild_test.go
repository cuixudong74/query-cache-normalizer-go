package qcache

import (
	"reflect"
	"testing"
)

func TestRebuildMissingFieldsMarked(t *testing.T) {
	c := New(defaultConfig())
	// Write name only.
	q1 := userQ("A", "", int64(1), "id", "name")
	if _, err := c.Write(q1, &Response{Data: map[string]any{
		"user": map[string]any{"__typename": "User", "id": int64(1), "name": "Ada"},
	}}); err != nil {
		t.Fatal(err)
	}
	// Read a shape requesting email that was never cached.
	q2 := userQ("B", "", int64(1), "id", "name", "email")
	r, err := c.ReadQuery(q2)
	if err != nil {
		t.Fatal(err)
	}
	if r.Complete {
		t.Fatal("query with uncached field must be incomplete")
	}
	u := r.Data["user"].(map[string]any)
	switch m := u["email"].(type) {
	case MissingMarker:
		if m.MarkerKind() != MarkMissing {
			t.Fatalf("kind = %v", m.MarkerKind())
		}
	default:
		t.Fatalf("email = %T, want MissingMarker", u["email"])
	}
	if u["name"] != "Ada" {
		t.Fatalf("name = %v", u["name"])
	}
	if len(r.Missing) == 0 {
		t.Fatal("Missing paths should be reported")
	}
}

func TestRebuildRoundTripEqualsRefetch(t *testing.T) {
	c := New(defaultConfig())
	q := &Query{Name: "Me", Type: OpQuery, Selections: []Selection{
		{Name: "viewer", Selections: []Selection{
			{Name: "id"}, {Name: "name"},
			{Name: "company", Selections: []Selection{{Name: "id"}, {Name: "name"}}},
		}},
	}}
	fresh := &Response{Data: map[string]any{
		"viewer": map[string]any{
			"__typename": "User", "id": int64(9), "name": "Ada",
			"company": map[string]any{"__typename": "Org", "id": int64(3), "name": "Analytical"},
		},
	}}
	// Rebuild must equal the server response projected to the selected
	// document (__typename is an identity hint, not a selection).
	want := map[string]any{
		"viewer": map[string]any{
			"id": int64(9), "name": "Ada",
			"company": map[string]any{"id": int64(3), "name": "Analytical"},
		},
	}
	if _, err := c.Write(q, fresh); err != nil {
		t.Fatal(err)
	}
	r, err := c.ReadQuery(q)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.Data, want) {
		t.Fatalf("rebuilt != refetch\n got %v\nwant %v", r.Data, want)
	}
}

func TestRebuildListAndNoCrossPollution(t *testing.T) {
	c := New(defaultConfig())
	// A list query normalizes each element by identity.
	listQ := &Query{Name: "Users", Type: OpQuery, Selections: []Selection{
		{Name: "users", Selections: []Selection{{Name: "id"}, {Name: "name"}}},
	}}
	if _, err := c.Write(listQ, &Response{Data: map[string]any{
		"users": []any{
			map[string]any{"__typename": "User", "id": int64(1), "name": "Ada"},
			map[string]any{"__typename": "User", "id": int64(2), "name": "Lin"},
		},
	}}); err != nil {
		t.Fatal(err)
	}
	r, _ := c.ReadQuery(listQ)
	users := r.Data["users"].([]any)
	if len(users) != 2 {
		t.Fatalf("list len = %d", len(users))
	}
	first := users[0].(map[string]any)
	if first["name"] != "Ada" {
		t.Fatalf("first = %v", first)
	}
}

func TestUnionFragmentsDoNotPollute(t *testing.T) {
	c := New(defaultConfig())
	q := &Query{Name: "Feed", Type: OpQuery, Selections: []Selection{
		{Name: "feed", Selections: []Selection{
			{Name: "id"},
			{Name: "title", TypeName: "Post"},
			{Name: "body", TypeName: "Post"},
			{Name: "text", TypeName: "Comment"},
		}},
	}}
	if _, err := c.Write(q, &Response{Data: map[string]any{
		"feed": []any{
			map[string]any{"__typename": "Post", "id": int64(1), "title": "T", "body": "B"},
			map[string]any{"__typename": "Comment", "id": int64(2), "text": "hello"},
		},
	}}); err != nil {
		t.Fatal(err)
	}
	r, err := c.ReadQuery(q)
	if err != nil {
		t.Fatal(err)
	}
	feed := r.Data["feed"].([]any)
	post := feed[0].(map[string]any)
	if post["title"] != "T" {
		t.Fatalf("post title = %v", post["title"])
	}
	if _, leaked := post["text"]; leaked {
		t.Fatalf("Comment.text leaked into Post: %v", post)
	}
	comment := feed[1].(map[string]any)
	if comment["text"] != "hello" {
		t.Fatalf("comment text = %v", comment["text"])
	}
	if _, leaked := comment["title"]; leaked {
		t.Fatalf("Post.title leaked into Comment: %v", comment)
	}
}

func TestDifferentFieldShapesDoNotPollute(t *testing.T) {
	c := New(defaultConfig())
	thin := userQ("Thin", "", int64(1), "id", "name")
	wide := userQ("Wide", "", int64(1), "id", "email")
	if _, err := c.Write(thin, &Response{Data: map[string]any{
		"user": map[string]any{"__typename": "User", "id": int64(1), "name": "Ada"},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Write(wide, &Response{Data: map[string]any{
		"user": map[string]any{"__typename": "User", "id": int64(1), "email": "a@x"},
	}}); err != nil {
		t.Fatal(err)
	}
	// Thin read must not surface email; wide read must not surface name.
	rt, _ := c.ReadQuery(thin)
	if _, ok := rt.Data["user"].(map[string]any)["email"]; ok {
		t.Fatal("email leaked into thin shape")
	}
	rw, _ := c.ReadQuery(wide)
	if _, ok := rw.Data["user"].(map[string]any)["name"]; ok {
		t.Fatal("name leaked into wide shape")
	}
}
