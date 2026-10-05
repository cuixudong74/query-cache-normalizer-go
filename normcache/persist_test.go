package normcache

import (
	"bytes"
	"reflect"
	"testing"
)

// 固定操作序列，用于确定性验证。
func runOpSequence(c *Cache) {
	q1 := mustParse0(`query Q($id: ID!) { user(id: $id) { id name friends { id name } } }`)
	c.Write(q1, map[string]any{"id": "1"}, map[string]any{
		"user": map[string]any{
			"__typename": "User", "id": "1", "name": "Ann",
			"friends": []any{
				map[string]any{"__typename": "User", "id": "2", "name": "Ben"},
			},
		},
	}, nil)
	q2 := mustParse0(`{ user(id: "2") { id email } }`)
	c.Write(q2, nil, map[string]any{
		"user": map[string]any{"__typename": "User", "id": "2", "email": nil},
	}, []ResponseError{{Path: []any{"user", "email"}, Message: "denied"}})
	c.InvalidateField("User:1", "name")
}

func mustParse0(src string) SelectionSet {
	q, err := ParseQuery(src)
	if err != nil {
		panic(err)
	}
	return q
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	c := New()
	runOpSequence(c)

	var buf bytes.Buffer
	if err := c.Snapshot(&buf); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	restored, err := Restore(&buf)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}

	// 记录内容一致
	if !reflect.DeepEqual(c.records, restored.records) {
		t.Fatalf("records differ after restore\nwant %+v\ngot  %+v", c.records, restored.records)
	}

	// 读取结果一致：Errored 与 Missing 标记都保留
	q := mustParse0(`query Q($id: ID!) { user(id: $id) { id name friends { id name } } }`)
	before := c.Read(q, map[string]any{"id": "1"})
	after := restored.Read(q, map[string]any{"id": "1"})
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("read differs after restore\nbefore %+v\nafter  %+v", before, after)
	}
	if _, ok := after.Data["user"].(map[string]any)["name"].(Missing); !ok {
		t.Fatalf("invalidated field should read Missing after restore: %+v", after.Data)
	}
}

func TestSnapshotDeterministic(t *testing.T) {
	var snapshots []string
	for i := 0; i < 3; i++ {
		c := New()
		runOpSequence(c)
		var buf bytes.Buffer
		if err := c.Snapshot(&buf); err != nil {
			t.Fatalf("snapshot: %v", err)
		}
		snapshots = append(snapshots, buf.String())
	}
	for i := 1; i < len(snapshots); i++ {
		if snapshots[i] != snapshots[0] {
			t.Fatalf("snapshot %d differs from snapshot 0", i)
		}
	}
}

// 相同操作序列在恢复后继续执行，可观测结果逐条一致。
func TestObservableSequenceIdenticalAfterRestore(t *testing.T) {
	build := func() *Cache {
		c := New()
		runOpSequence(c)
		return c
	}
	observe := func(c *Cache) []ReadResult {
		var out []ReadResult
		out = append(out, c.Read(mustParse0(`query Q($id: ID!) { user(id: $id) { id name friends { id name } } }`), map[string]any{"id": "1"}))
		out = append(out, c.Read(mustParse0(`{ user(id: "2") { id email } }`), nil))
		return out
	}

	direct := build()
	var buf bytes.Buffer
	if err := direct.Snapshot(&buf); err != nil {
		t.Fatal(err)
	}
	restored, err := Restore(&buf)
	if err != nil {
		t.Fatal(err)
	}

	a, b := observe(direct), observe(restored)
	if len(a) != len(b) {
		t.Fatal("observation count mismatch")
	}
	for i := range a {
		if !reflect.DeepEqual(a[i], b[i]) {
			t.Fatalf("observation %d differs:\n%+v\n%+v", i, a[i], b[i])
		}
	}
}
