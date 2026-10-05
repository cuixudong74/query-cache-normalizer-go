package qcache

import (
	"fmt"
	"sync"
	"testing"
)

// TestConcurrentSnapshotIsolation hammers the cache with writers and readers.
// Every read must be a complete, committed snapshot: either the entity is fully
// consistent (name/email belonging to the same write batch) or absent; a reader
// can never observe a partially merged entity.
func TestConcurrentSnapshotIsolation(t *testing.T) {
	c := New(defaultConfig())
	base := userQ("Q", "", int64(1), "id", "name", "email")

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Pre-populate so readers have something consistent to observe.
	if _, err := c.Write(base, &Response{Data: map[string]any{
		"user": map[string]any{"__typename": "User", "id": int64(1), "name": "n0", "email": "e0"},
	}}); err != nil {
		t.Fatal(err)
	}

	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				tag := fmt.Sprintf("n%d-%d", w, i)
				etag := fmt.Sprintf("e%d-%d", w, i)
				_, _ = c.Write(base, &Response{Data: map[string]any{
					"user": map[string]any{"__typename": "User", "id": int64(1), "name": tag, "email": etag},
				}})
			}
		}(w)
	}

	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				res, err := c.ReadQuery(base)
				if err != nil {
					t.Errorf("read: %v", err)
					return
				}
				u, ok := res.Data["user"].(map[string]any)
				if !ok {
					t.Errorf("user missing mid-flight")
					return
				}
				name, _ := u["name"].(string)
				email, _ := u["email"].(string)
				// A committed pair always shares the same writer/iteration tag.
				if name == "" || email == "" {
					t.Errorf("partial field visibility: %v", u)
					return
				}
				if name[1:] != email[1:] { // strip n/e prefix, compare tags
					t.Errorf("torn snapshot across writes: name=%s email=%s", name, email)
					return
				}
			}
		}()
	}

	// Run briefly under race detector.
	for i := 0; i < 200000; i++ {
	}
	close(stop)
	wg.Wait()
}
