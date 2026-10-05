package normcache

import "testing"

// 部分失败：带错误的字段不写入、不污染已有缓存，并以 Errored 标记与缺失区分。
func TestPartialErrorDoesNotPollute(t *testing.T) {
	c := New()
	q := mustParse(t, `{ user(id: "1") { id name email } }`)
	// 先写入完整数据
	c.Write(q, nil, map[string]any{
		"user": map[string]any{"__typename": "User", "id": "1", "name": "Ann", "email": "a@x.com"},
	}, nil)

	// 第二次响应 email 字段带错误（数据为 null）
	c.Write(q, nil, map[string]any{
		"user": map[string]any{"__typename": "User", "id": "1", "name": "Ann", "email": nil},
	}, []ResponseError{
		{Path: []any{"user", "email"}, Message: "permission denied"},
	})

	res := c.Read(q, nil)
	if res.Complete {
		t.Fatal("expected incomplete due to errored field")
	}
	user := res.Data["user"].(map[string]any)
	if user["name"] != "Ann" {
		t.Fatalf("unaffected field polluted: %+v", user)
	}
	e, ok := user["email"].(Errored)
	if !ok {
		t.Fatalf("email should be Errored, got %#v", user["email"])
	}
	if e.Message != "permission denied" {
		t.Fatalf("bad error message: %+v", e)
	}
	// Errored 与 Missing 可区分
	if _, isMissing := user["email"].(Missing); isMissing {
		t.Fatal("errored field must not look missing")
	}
}

// 列表中某个元素字段失败：只标记该元素字段。
func TestErrorPathWithListIndex(t *testing.T) {
	c := New()
	q := mustParse(t, `{ users { id name } }`)
	c.Write(q, nil, map[string]any{
		"users": []any{
			map[string]any{"__typename": "User", "id": "1", "name": "Ann"},
			map[string]any{"__typename": "User", "id": "2", "name": nil},
		},
	}, []ResponseError{
		{Path: []any{"users", 1, "name"}, Message: "boom"},
	})

	res := c.Read(q, nil)
	users := res.Data["users"].([]any)
	if users[0].(map[string]any)["name"] != "Ann" {
		t.Fatalf("first user polluted: %+v", users[0])
	}
	if _, ok := users[1].(map[string]any)["name"].(Errored); !ok {
		t.Fatalf("second user name should be Errored: %+v", users[1])
	}
	// 第一个用户的记录不受第二个影响
	if _, ok := c.records["User:1"].Fields["name"].(Errored); ok {
		t.Fatal("error leaked across entities")
	}
}
