package qcache

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestMemorySourceAndFetch(t *testing.T) {
	src := NewMemorySource()
	q := userQ("U", "", int64(1), "id", "name")
	resp := &Response{Data: map[string]any{
		"user": map[string]any{"__typename": "User", "id": int64(1), "name": "Ada"},
	}}
	src.Set(q, resp)

	c := New(defaultConfig())
	got, wr, err := c.Fetch(context.Background(), src, q)
	if err != nil {
		t.Fatal(err)
	}
	if got != resp {
		t.Fatal("Fetch returned unexpected response pointer")
	}
	if wr.Version != 1 {
		t.Fatalf("version = %d", wr.Version)
	}
	r, _ := c.ReadQuery(q)
	if r.Data["user"].(map[string]any)["name"] != "Ada" {
		t.Fatalf("fetched data = %v", r.Data)
	}
}

func TestFileSource(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fixtures.json")
	q := userQ("U", "", int64(1), "id", "name")
	doc := map[string]any{
		"responses": map[string]any{
			queryFingerprint(q): map[string]any{
				"data": map[string]any{
					"user": map[string]any{"__typename": "User", "id": 1, "name": "Ada"},
				},
			},
		},
	}
	raw := canonicalJSON(doc)
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	fs, err := NewFileSource(path)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := fs.Execute(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	c := New(defaultConfig())
	if _, err := c.Write(q, resp); err != nil {
		t.Fatal(err)
	}
	r, _ := c.ReadQuery(q)
	// File JSON decodes id as number; name is the asserted string.
	if r.Data["user"].(map[string]any)["name"] != "Ada" {
		t.Fatalf("file-sourced data = %v", r.Data)
	}
}
