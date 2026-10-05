package normcache_test

import (
	"fmt"

	normcache "github.com/cuixudong74/query-cache-normalizer-go/normcache"
)

// 演示：两次查询返回同一实体的不同字段，缓存合并为一份记录，
// 任一查询都能从缓存完整重建结果。
func Example() {
	c := normcache.New()

	q1, _ := normcache.ParseQuery(`query GetUser($id: ID!) { user(id: $id) { id name } }`)
	q2, _ := normcache.ParseQuery(`{ user(id: "1") { id email } }`)

	c.Write(q1, map[string]any{"id": "1"}, map[string]any{
		"user": map[string]any{"__typename": "User", "id": "1", "name": "Ann"},
	}, nil)
	c.Write(q2, nil, map[string]any{
		"user": map[string]any{"__typename": "User", "id": "1", "email": "ann@example.com"},
	}, nil)

	r1 := c.Read(q1, map[string]any{"id": "1"})
	fmt.Println(r1.Complete, r1.Data["user"].(map[string]any)["name"])

	r2 := c.Read(q2, nil)
	fmt.Println(r2.Complete, r2.Data["user"].(map[string]any)["email"])
	// Output:
	// true Ann
	// true ann@example.com
}
