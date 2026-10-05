package normcache

import "testing"

func mustParse(t *testing.T, src string) SelectionSet {
	t.Helper()
	q, err := ParseQuery(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return q
}

func TestWriteReadRoundTrip(t *testing.T) {
	c := New()
	q := mustParse(t, `{ user(id: "1") { id name } }`)
	c.Write(q, nil, map[string]any{
		"user": map[string]any{"__typename": "User", "id": "1", "name": "Ann"},
	}, nil)

	res := c.Read(q, nil)
	if !res.Complete {
		t.Fatalf("expected complete, got %+v", res.Data)
	}
	user := res.Data["user"].(map[string]any)
	if user["name"] != "Ann" || user["id"] != "1" {
		t.Fatalf("bad user: %+v", user)
	}
}

func TestEntityMergeAcrossQueries(t *testing.T) {
	c := New()
	q1 := mustParse(t, `{ user(id: "1") { id name } }`)
	q2 := mustParse(t, `{ user(id: "1") { id age } }`)
	c.Write(q1, nil, map[string]any{
		"user": map[string]any{"__typename": "User", "id": "1", "name": "Ann"},
	}, nil)
	c.Write(q2, nil, map[string]any{
		"user": map[string]any{"__typename": "User", "id": "1", "age": int64(30)},
	}, nil)

	// 合并后：任一查询都可完整读出
	if r := c.Read(q1, nil); !r.Complete {
		t.Fatalf("q1 incomplete: %+v", r.Data)
	}
	if r := c.Read(q2, nil); !r.Complete {
		t.Fatalf("q2 incomplete: %+v", r.Data)
	}
	// 记录只有一份
	if len(c.records) != 2 { // ROOT_QUERY + User:1
		t.Fatalf("expected 2 records, got %d", len(c.records))
	}
	rec := c.records["User:1"]
	if rec.Fields["name"] != "Ann" || rec.Fields["age"] != int64(30) {
		t.Fatalf("bad merged record: %+v", rec.Fields)
	}
}

func TestMissingMarkedNotNull(t *testing.T) {
	c := New()
	q := mustParse(t, `{ user(id: "1") { id name email } }`)
	c.Write(q, nil, map[string]any{
		"user": map[string]any{"__typename": "User", "id": "1", "name": "Ann"},
		// email 未返回
	}, nil)
	res := c.Read(q, nil)
	if res.Complete {
		t.Fatal("expected incomplete")
	}
	user := res.Data["user"].(map[string]any)
	m, ok := user["email"].(Missing)
	if !ok {
		t.Fatalf("email should be Missing, got %#v", user["email"])
	}
	if m.Field != "email" {
		t.Fatalf("bad missing field: %+v", m)
	}
}
