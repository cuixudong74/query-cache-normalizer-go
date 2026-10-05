package qcache

import (
	"errors"
	"testing"
)

func TestTopLevelFailureDoesNotPollute(t *testing.T) {
	c := New(defaultConfig())
	q := userQ("U", "", int64(1), "id", "name")
	if _, err := c.Write(q, &Response{Data: map[string]any{
		"user": map[string]any{"__typename": "User", "id": int64(1), "name": "Ada"},
	}}); err != nil {
		t.Fatal(err)
	}
	before := c.cur.Load().seq

	_, err := c.Write(q, &Response{Errors: []FieldError{{Message: "boom", Path: []any{"user"}}}})
	if !errors.Is(err, ErrTopLevelFailure) {
		t.Fatalf("err = %v, want ErrTopLevelFailure", err)
	}
	if c.cur.Load().seq != before {
		t.Fatal("top-level failure must not commit a new snapshot")
	}
	r, _ := c.ReadQuery(q)
	u := r.Data["user"].(map[string]any)
	if u["name"] != "Ada" {
		t.Fatalf("existing cache polluted by failed response: %v", u["name"])
	}
}

func TestPartialFailureMarksSkippedNotMissing(t *testing.T) {
	c := New(defaultConfig())
	q := userQ("U", "", int64(1), "id", "name", "email")
	resp := &Response{
		Data: map[string]any{
			"user": map[string]any{"__typename": "User", "id": int64(1), "name": "Ada"},
		},
		Errors: []FieldError{
			{Message: "forbidden", Path: []any{"user", "email"}},
		},
	}
	wr, err := c.Write(q, resp)
	if err != nil {
		t.Fatal(err)
	}
	if wr.Complete {
		t.Fatal("partial-failure write must be incomplete")
	}
	if len(wr.Skipped) == 0 {
		t.Fatal("skipped paths must be reported")
	}
	r, _ := c.ReadQuery(q)
	u := r.Data["user"].(map[string]any)
	switch m := u["email"].(type) {
	case SkippedMarker:
		if m.Error.Message != "forbidden" {
			t.Fatalf("skip error = %v", m.Error)
		}
	default:
		t.Fatalf("email = %T, want SkippedMarker", u["email"])
	}
	if len(r.Missing) != 0 {
		t.Fatalf("skipped field must not also be missing: %v", r.Missing)
	}

	// A later healthy response repairs the skip (present overwrites skipped).
	if _, err := c.Write(q, &Response{Data: map[string]any{
		"user": map[string]any{"__typename": "User", "id": int64(1), "name": "Ada", "email": "a@x"},
	}}); err != nil {
		t.Fatal(err)
	}
	r2, _ := c.ReadQuery(q)
	u2 := r2.Data["user"].(map[string]any)
	if u2["email"] != "a@x" || !r2.Complete {
		t.Fatalf("repair failed: %v complete=%v", u2["email"], r2.Complete)
	}
}

func TestSkippedNeverOverwritesPresent(t *testing.T) {
	c := New(defaultConfig())
	q := userQ("U", "", int64(1), "id", "name", "email")
	// Good value first.
	if _, err := c.Write(q, &Response{Data: map[string]any{
		"user": map[string]any{"__typename": "User", "id": int64(1), "name": "Ada", "email": "a@x"},
	}}); err != nil {
		t.Fatal(err)
	}
	// Later response errors on email. Skip must not erase the good value.
	if _, err := c.Write(q, &Response{
		Data:   map[string]any{"user": map[string]any{"__typename": "User", "id": int64(1), "name": "Ada"}},
		Errors: []FieldError{{Message: "down", Path: []any{"user", "email"}}},
	}); err != nil {
		t.Fatal(err)
	}
	r, _ := c.ReadQuery(q)
	u := r.Data["user"].(map[string]any)
	if u["email"] != "a@x" {
		t.Fatalf("skip overwrote present value: %v", u["email"])
	}
}

func TestListElementErrorSkipped(t *testing.T) {
	c := New(defaultConfig())
	q := &Query{Name: "List", Type: OpQuery, Selections: []Selection{
		{Name: "users", Selections: []Selection{{Name: "id"}, {Name: "name"}}},
	}}
	resp := &Response{
		Data: map[string]any{
			"users": []any{
				map[string]any{"__typename": "User", "id": int64(1), "name": "Ada"},
				map[string]any{"__typename": "User", "id": int64(2)},
			},
		},
		Errors: []FieldError{{Message: "x", Path: []any{"users", 1, "name"}}},
	}
	if _, err := c.Write(q, resp); err != nil {
		t.Fatal(err)
	}
	r, _ := c.ReadQuery(q)
	users := r.Data["users"].([]any)
	switch users[1].(map[string]any)["name"].(type) {
	case SkippedMarker:
	default:
		t.Fatalf("elem name = %T", users[1].(map[string]any)["name"])
	}
}
