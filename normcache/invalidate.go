package normcache

import "sort"

// InvalidateEntity 使某个实体失效：删除其记录。
// 之后任何引用该实体的读取都会得到 Missing 标记，
// 与“重新请求才能拿到数据”的语义一致。
func (c *Cache) InvalidateEntity(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.records, key)
}

// InvalidateField 使某个实体的指定字段失效（fieldKey 可用 FieldKey 生成，
// 含参数化形式如 `friends({"first":10})`）。
// 之后读取该字段得到 Missing，其余字段不受影响。
func (c *Cache) InvalidateField(key, fieldKey string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if rec, ok := c.records[key]; ok {
		delete(rec.Fields, fieldKey)
	}
}

// GC 回收不再被根查询传递引用的实体记录（mark-sweep）。
// 返回被回收的实体键（排序后，确定性）。根记录本身永不回收。
func (c *Cache) GC() []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	reachable := map[string]bool{RootKey: true}
	var visit func(key string)
	visit = func(key string) {
		rec, ok := c.records[key]
		if !ok {
			return
		}
		for _, v := range rec.Fields {
			for _, ref := range refsIn(v) {
				if !reachable[ref.Key] {
					reachable[ref.Key] = true
					visit(ref.Key)
				}
			}
		}
	}
	visit(RootKey)

	var removed []string
	for key := range c.records {
		if !reachable[key] {
			delete(c.records, key)
			removed = append(removed, key)
		}
	}
	sort.Strings(removed)
	return removed
}

// refsIn 收集值中（含列表、内嵌对象内）的所有实体引用。
func refsIn(v any) []Reference {
	switch t := v.(type) {
	case Reference:
		return []Reference{t}
	case []any:
		var out []Reference
		for _, e := range t {
			out = append(out, refsIn(e)...)
		}
		return out
	case map[string]any:
		var out []Reference
		for _, e := range t {
			out = append(out, refsIn(e)...)
		}
		return out
	default:
		return nil
	}
}
