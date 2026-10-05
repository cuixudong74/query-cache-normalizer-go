package normcache

import (
	"fmt"
	"sync"
	"testing"
)

// 并发读写：读到的每个实体必须是某个已提交写入的完整快照，
// 不允许看到 name 来自第 i 次写入而 age 来自第 j 次写入的混合状态。
func TestConcurrentReadWrite(t *testing.T) {
	c := New()
	q := mustParse(t, `{ user(id: "1") { id name age } }`)
	c.Write(q, nil, map[string]any{
		"user": map[string]any{"__typename": "User", "id": "1", "name": "v0", "age": int64(0)},
	}, nil)

	var writers, readers sync.WaitGroup
	stop := make(chan struct{})

	// 写方：每次写入 name 与 age 版本一致
	for w := 0; w < 4; w++ {
		writers.Add(1)
		go func(w int) {
			defer writers.Done()
			for i := 1; i <= 200; i++ {
				v := w*1000 + i
				c.Write(q, nil, map[string]any{
					"user": map[string]any{
						"__typename": "User", "id": "1",
						"name": fmt.Sprintf("v%d", v),
						"age":  int64(v),
					},
				}, nil)
			}
		}(w)
	}

	// 读方：校验快照一致性
	for r := 0; r < 8; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				res := c.Read(q, nil)
				user, ok := res.Data["user"].(map[string]any)
				if !ok {
					continue
				}
				name, _ := user["name"].(string)
				age, _ := user["age"].(int64)
				var n int64
				if _, err := fmt.Sscanf(name, "v%d", &n); err == nil {
					if n != age {
						t.Errorf("torn read: name=%s age=%d", name, age)
						return
					}
				}
			}
		}()
	}

	writers.Wait()
	close(stop)
	readers.Wait()
}

// 并发下同时写入不同实体与失效、GC 不应数据竞争（配合 -race 运行）。
func TestConcurrentMixedOps(t *testing.T) {
	c := New()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			id := fmt.Sprintf("%d", g%4)
			q := mustParse(t, fmt.Sprintf(`{ user(id: %q) { id name } }`, id))
			for i := 0; i < 100; i++ {
				c.Write(q, nil, map[string]any{
					"user": map[string]any{"__typename": "User", "id": id, "name": "n"},
				}, nil)
				c.Read(q, nil)
				if i%10 == 0 {
					c.InvalidateField("User:"+id, "name")
				}
				if i%25 == 0 {
					c.GC()
				}
			}
		}(g)
	}
	wg.Wait()
}
