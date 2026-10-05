package normcache

// ReadResult 是重建的查询结果。
type ReadResult struct {
	Data     map[string]any `json:"data"`
	Complete bool           `json:"complete"` // 所有字段均命中且无错误标记时为 true
}

// Read 从缓存重建查询结果。
//
//   - 缺失字段以 Missing 标记放入结果（绝不用 nil 冒充），Complete 为 false；
//   - 上次写入带错误的字段以 Errored 标记放入结果；
//   - 读取在读锁内完成，任一时刻看到的是某个已提交写入的完整快照。
func (c *Cache) Read(query SelectionSet, vars map[string]any) ReadResult {
	c.mu.RLock()
	defer c.mu.RUnlock()

	r := &reader{cache: c, vars: vars, complete: true}
	data := r.readSelectionSet(RootKey, query)
	return ReadResult{Data: data, Complete: r.complete}
}

type reader struct {
	cache    *Cache
	vars     map[string]any
	complete bool
}

func (r *reader) miss(field string) Missing {
	r.complete = false
	return Missing{Field: field}
}

func (r *reader) readSelectionSet(key string, set SelectionSet) map[string]any {
	out := map[string]any{}
	rec, ok := r.cache.records[key]
	if !ok {
		// 整条记录缺失：所有字段标记 Missing。
		for _, sel := range set.Selections {
			if sel.Field != nil {
				out[sel.Field.ResponseName()] = r.miss(FieldKey(sel.Field, r.vars))
			} else if sel.Fragment != nil {
				for k, v := range r.readSelectionSet(key, sel.Fragment.Selection) {
					out[k] = v
				}
			}
		}
		return out
	}
	r.readInto(rec, set, out)
	return out
}

func (r *reader) readInto(rec *Record, set SelectionSet, out map[string]any) {
	for _, sel := range set.Selections {
		switch {
		case sel.Field != nil:
			f := sel.Field
			storeKey := FieldKey(f, r.vars)
			val, ok := rec.Fields[storeKey]
			if !ok {
				out[f.ResponseName()] = r.miss(storeKey)
				continue
			}
			if e, isErr := val.(Errored); isErr {
				r.complete = false
				out[f.ResponseName()] = e
				continue
			}
			out[f.ResponseName()] = r.denormalizeValue(f.Selection, val, storeKey)
		case sel.Fragment != nil:
			if rec.TypeName == "" || rec.TypeName == sel.Fragment.TypeCondition {
				r.readInto(rec, sel.Fragment.Selection, out)
			}
		}
	}
}

func (r *reader) denormalizeValue(set SelectionSet, val any, field string) any {
	switch v := val.(type) {
	case Reference:
		child, ok := r.cache.records[v.Key]
		if !ok {
			return r.miss(field)
		}
		out := map[string]any{}
		if child.TypeName != "" {
			out[r.cache.opts.TypenameField] = child.TypeName
		}
		r.readInto(child, set, out)
		return out
	case []any:
		out := make([]any, len(v))
		for i, e := range v {
			out[i] = r.denormalizeValue(set, e, field)
		}
		return out
	case map[string]any:
		// 内嵌对象：按选择集从 map 中取值，并解析其中的引用。
		out := map[string]any{}
		r.denormalizeEmbedded(set, v, out, field)
		return out
	default:
		return val
	}
}

func (r *reader) denormalizeEmbedded(set SelectionSet, src, dst map[string]any, field string) {
	for k, v := range src {
		dst[k] = v
	}
	for _, sel := range set.Selections {
		switch {
		case sel.Field != nil:
			f := sel.Field
			raw, ok := src[f.ResponseName()]
			if !ok {
				continue
			}
			dst[f.ResponseName()] = r.denormalizeValue(f.Selection, raw, field)
		case sel.Fragment != nil:
			tn, _ := src["__typename"].(string)
			if tn == "" || tn == sel.Fragment.TypeCondition {
				r.denormalizeEmbedded(sel.Fragment.Selection, src, dst, field)
			}
		}
	}
}
