package normcache

import "testing"

// 别名与变量：同一实体在不同别名/变量下写入，必须合并到同一记录。
func TestAliasAndVariablesMerge(t *testing.T) {
	c := New()
	q1 := mustParse(t, `query A($id: ID!) { me: user(id: $id) { id name } }`)
	q2 := mustParse(t, `query B($uid: ID!) { admin: user(id: $uid) { id age } }`)
	c.Write(q1, map[string]any{"id": "1"}, map[string]any{
		"me": map[string]any{"__typename": "User", "id": "1", "name": "Ann"},
	}, nil)
	c.Write(q2, map[string]any{"uid": "1"}, map[string]any{
		"admin": map[string]any{"__typename": "User", "id": "1", "age": int64(30)},
	}, nil)

	if len(c.records) != 2 {
		t.Fatalf("expected 2 records, got %d: %v", len(c.records), c.records)
	}
	rec := c.records["User:1"]
	if rec.Fields["name"] != "Ann" || rec.Fields["age"] != int64(30) {
		t.Fatalf("merge failed: %+v", rec.Fields)
	}
	// 根字段键与变量名无关：user({"id":"1"})
	if _, ok := c.records[RootKey].Fields[`user({"id":"1"})`]; !ok {
		t.Fatalf("root field key mismatch: %+v", c.records[RootKey].Fields)
	}
	// 两种形态都能完整读回
	if r := c.Read(q1, map[string]any{"id": "1"}); !r.Complete {
		t.Fatalf("q1 incomplete: %+v", r.Data)
	}
	if r := c.Read(q2, map[string]any{"uid": "1"}); !r.Complete {
		t.Fatalf("q2 incomplete: %+v", r.Data)
	}
}

// 字段冲突：后写胜出。
func TestFieldConflictLastWriteWins(t *testing.T) {
	c := New()
	q := mustParse(t, `{ user(id: "1") { id name } }`)
	c.Write(q, nil, map[string]any{
		"user": map[string]any{"__typename": "User", "id": "1", "name": "Ann"},
	}, nil)
	c.Write(q, nil, map[string]any{
		"user": map[string]any{"__typename": "User", "id": "1", "name": "Bob"},
	}, nil)
	res := c.Read(q, nil)
	if res.Data["user"].(map[string]any)["name"] != "Bob" {
		t.Fatalf("expected last write wins, got %+v", res.Data)
	}
}

// 嵌套引用与列表。
func TestNestedAndList(t *testing.T) {
	c := New()
	q := mustParse(t, `{
		user(id: "1") {
			id
			name
			friends { id name }
		}
	}`)
	c.Write(q, nil, map[string]any{
		"user": map[string]any{
			"__typename": "User", "id": "1", "name": "Ann",
			"friends": []any{
				map[string]any{"__typename": "User", "id": "2", "name": "Ben"},
				map[string]any{"__typename": "User", "id": "3", "name": "Cat"},
			},
		},
	}, nil)

	res := c.Read(q, nil)
	if !res.Complete {
		t.Fatalf("incomplete: %+v", res.Data)
	}
	user := res.Data["user"].(map[string]any)
	friends := user["friends"].([]any)
	if friends[0].(map[string]any)["name"] != "Ben" || friends[1].(map[string]any)["name"] != "Cat" {
		t.Fatalf("bad friends: %+v", friends)
	}
	// 朋友是独立实体记录
	if _, ok := c.records["User:2"]; !ok {
		t.Fatal("User:2 should be its own record")
	}
	// 通过朋友实体更新名字，列表读取应反映新值（单一副本）
	c.Write(mustParse(t, `{ user(id: "2") { id name } }`), nil, map[string]any{
		"user": map[string]any{"__typename": "User", "id": "2", "name": "Benny"},
	}, nil)
	res2 := c.Read(q, nil)
	updated := res2.Data["user"].(map[string]any)["friends"].([]any)[0].(map[string]any)["name"]
	if updated != "Benny" {
		t.Fatalf("expected Benny, got %v", updated)
	}
}

// 联合类型：内联片段按 __typename 应用，不同形态互不污染。
func TestUnionTypes(t *testing.T) {
	c := New()
	q := mustParse(t, `{
		search(q: "x") {
			... on User { id name }
			... on Org { id title }
		}
	}`)
	c.Write(q, nil, map[string]any{
		"search": []any{
			map[string]any{"__typename": "User", "id": "1", "name": "Ann"},
			map[string]any{"__typename": "Org", "id": "9", "title": "Acme"},
		},
	}, nil)

	res := c.Read(q, nil)
	if !res.Complete {
		t.Fatalf("incomplete: %+v", res.Data)
	}
	items := res.Data["search"].([]any)
	u := items[0].(map[string]any)
	o := items[1].(map[string]any)
	if u["name"] != "Ann" || u["__typename"] != "User" {
		t.Fatalf("bad user: %+v", u)
	}
	if o["title"] != "Acme" || o["__typename"] != "Org" {
		t.Fatalf("bad org: %+v", o)
	}
	// User 记录不应有 title 字段，Org 不应有 name
	if _, ok := c.records["User:1"].Fields["title"]; ok {
		t.Fatal("union pollution: User has title")
	}
	if _, ok := c.records["Org:9"].Fields["name"]; ok {
		t.Fatal("union pollution: Org has name")
	}
}

// 参数化字段：不同参数值是不同字段键，互不覆盖。
func TestParameterizedFields(t *testing.T) {
	c := New()
	q := mustParse(t, `{ user(id: "1") { id friends(first: 2) { id } friends(first: 10) { id } } }`)
	c.Write(q, nil, map[string]any{
		"user": map[string]any{
			"__typename": "User", "id": "1",
			"friends": []any{map[string]any{"__typename": "User", "id": "2"}},
		},
	}, nil)
	rec := c.records["User:1"]
	if _, ok := rec.Fields[`friends({"first":2})`]; !ok {
		t.Fatalf("missing parameterized key: %+v", rec.Fields)
	}
	if _, ok := rec.Fields[`friends({"first":10})`]; !ok {
		t.Fatalf("missing parameterized key: %+v", rec.Fields)
	}
}
