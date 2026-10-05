package normcache

import "testing"

func TestInvalidateEntity(t *testing.T) {
	c := New()
	q := mustParse(t, `{ user(id: "1") { id name } }`)
	c.Write(q, nil, map[string]any{
		"user": map[string]any{"__typename": "User", "id": "1", "name": "Ann"},
	}, nil)

	c.InvalidateEntity("User:1")
	res := c.Read(q, nil)
	if res.Complete {
		t.Fatal("expected incomplete after invalidation")
	}
	if _, ok := res.Data["user"].(Missing); !ok {
		t.Fatalf("expected Missing for invalidated entity, got %#v", res.Data["user"])
	}
}

func TestInvalidateField(t *testing.T) {
	c := New()
	q := mustParse(t, `{ user(id: "1") { id name age } }`)
	c.Write(q, nil, map[string]any{
		"user": map[string]any{"__typename": "User", "id": "1", "name": "Ann", "age": int64(30)},
	}, nil)

	c.InvalidateField("User:1", "age")
	res := c.Read(q, nil)
	if res.Complete {
		t.Fatal("expected incomplete")
	}
	user := res.Data["user"].(map[string]any)
	if user["name"] != "Ann" {
		t.Fatalf("name should survive field invalidation: %+v", user)
	}
	if _, ok := user["age"].(Missing); !ok {
		t.Fatalf("age should be Missing, got %#v", user["age"])
	}
}

func TestGC(t *testing.T) {
	c := New()
	// user 1 引用 user 2；user 3 不被任何根查询引用（先写后改写根）
	q := mustParse(t, `{ user(id: "1") { id bestFriend { id name } } }`)
	c.Write(q, nil, map[string]any{
		"user": map[string]any{
			"__typename": "User", "id": "1",
			"bestFriend": map[string]any{"__typename": "User", "id": "2", "name": "Ben"},
		},
	}, nil)
	// 直接写入一个不被根引用的实体
	c.Write(mustParse(t, `{ user(id: "3") { id name } }`), nil, map[string]any{
		"user": map[string]any{"__typename": "User", "id": "3", "name": "Cat"},
	}, nil)
	// 改写根查询使 user 3 不再被引用：用另一条根字段覆盖
	c.Write(mustParse(t, `{ user(id: "1") { id bestFriend { id name } } }`), nil, map[string]any{
		"user": map[string]any{
			"__typename": "User", "id": "1",
			"bestFriend": map[string]any{"__typename": "User", "id": "2", "name": "Ben"},
		},
	}, nil)
	// 此时 User:3 仍被根字段 user({"id":"3"}) 引用；先失效该根字段
	c.InvalidateField(RootKey, `user({"id":"3"})`)

	removed := c.GC()
	if len(removed) != 1 || removed[0] != "User:3" {
		t.Fatalf("expected [User:3], got %v", removed)
	}
	// User:1、User:2 仍可达
	if _, ok := c.records["User:1"]; !ok {
		t.Fatal("User:1 should survive GC")
	}
	if _, ok := c.records["User:2"]; !ok {
		t.Fatal("User:2 should survive GC")
	}
}
