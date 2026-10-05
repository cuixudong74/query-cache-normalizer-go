package qcache_test

import (
	"context"
	"fmt"

	qcache "github.com/cuixudong74/query-cache-normalizer-go"
)

func Example() {
	c := qcache.New(qcache.Config{})

	// First query: user(id:1) with name.
	q1 := &qcache.Query{
		Name:      "Profile",
		Type:      qcache.OpQuery,
		Variables: map[string]any{"uid": int64(1)},
		Selections: []qcache.Selection{
			{Name: "user", Args: map[string]any{"id": qcache.Variable{Name: "uid"}},
				Selections: []qcache.Selection{{Name: "id"}, {Name: "name"}}},
		},
	}
	_, _ = c.Write(q1, &qcache.Response{Data: map[string]any{
		"user": map[string]any{"__typename": "User", "id": int64(1), "name": "Ada"},
	}})

	// Second query, different operation name and alias, adds email. Both map
	// to the single normalized record User:1.
	q2 := &qcache.Query{
		Name: "Contact",
		Type: qcache.OpQuery,
		Selections: []qcache.Selection{
			{Name: "user", Alias: "u", Args: map[string]any{"id": int64(1)},
				Selections: []qcache.Selection{{Name: "id"}, {Name: "email"}}},
		},
	}
	_, _ = c.Write(q2, &qcache.Response{Data: map[string]any{
		"u": map[string]any{"__typename": "User", "id": int64(1), "email": "ada@example.com"},
	}})

	r, _ := c.ReadQuery(q1)
	fmt.Println("profile name:", r.Data["user"].(map[string]any)["name"])

	snap, _ := c.Snapshot()
	fmt.Println("entities:", snap.Entities())

	// Simulated backend via Fetch.
	src := qcache.NewMemorySource()
	_ = src

	// Targeted field invalidation turns email into a distinguishable hole.
	_, _ = c.InvalidateField("User:1", "email")
	r2, _ := c.ReadQuery(q2)
	switch r2.Data["u"].(map[string]any)["email"].(type) {
	case qcache.MissingMarker:
		fmt.Println("email: missing after invalidation")
	}

	_ = context.Background
	// Output:
	// profile name: Ada
	// entities: [ROOT_QUERY User:1]
	// email: missing after invalidation
}
