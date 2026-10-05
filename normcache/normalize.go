package normcache

import (
	"fmt"
	"sort"
	"strings"
)

// RootKey 是根查询记录所用的保留实体键。
const RootKey = "ROOT_QUERY"

// ResponseError 描述响应中某个路径的错误（部分失败）。
type ResponseError struct {
	Path    []any  `json:"path"`
	Message string `json:"message"`
}

// WriteResult 报告一次写入的结果。
type WriteResult struct {
	Entities []string `json:"entities"` // 本次触及的实体键（排序后，确定性）
}

// entityKey 从对象中提取实体身份：类型字段 + 身份字段。无身份返回空串。
func entityKey(obj map[string]any, opts Options) string {
	tn, _ := obj[opts.TypenameField].(string)
	if tn == "" {
		return ""
	}
	id, ok := obj[opts.IDField]
	if !ok || id == nil {
		return ""
	}
	switch v := id.(type) {
	case string:
		if v == "" {
			return ""
		}
		return tn + ":" + v
	case int64:
		return fmt.Sprintf("%s:%d", tn, v)
	case int:
		return fmt.Sprintf("%s:%d", tn, v)
	case float64:
		return fmt.Sprintf("%s:%g", tn, v)
	}
	return ""
}

// pathString 把错误路径规范化为可比较字符串。
func pathString(path []any) string {
	parts := make([]string, len(path))
	for i, p := range path {
		switch v := p.(type) {
		case string:
			parts[i] = v
		case int:
			parts[i] = fmt.Sprintf("#%d", v)
		case int64:
			parts[i] = fmt.Sprintf("#%d", v)
		case float64:
			parts[i] = fmt.Sprintf("#%d", int64(v))
		default:
			parts[i] = fmt.Sprint(v)
		}
	}
	return strings.Join(parts, "/")
}

type errIndex struct {
	byPath map[string]string // pathString -> message
}

func newErrIndex(errs []ResponseError) *errIndex {
	e := &errIndex{byPath: map[string]string{}}
	for _, er := range errs {
		e.byPath[pathString(er.Path)] = er.Message
	}
	return e
}

// at 返回恰好位于该路径的错误消息。
func (e *errIndex) at(path []any) (string, bool) {
	m, ok := e.byPath[pathString(path)]
	return m, ok
}

// Write 将一次查询响应归一化写入缓存。
//
// 一致性规则：
//   - 同一实体（__typename+id）的字段合并进同一份记录；
//   - 字段冲突按“后写胜出”（last write wins），对相同操作序列结果确定；
//   - 带错误的字段不写入，记录 Errored 标记，与真正缺失区分；
//   - 整个写入在单把写锁内完成，读方只会看到写入前或写入后的完整快照。
func (c *Cache) Write(query SelectionSet, vars map[string]any, data map[string]any, errs []ResponseError) WriteResult {
	c.mu.Lock()
	defer c.mu.Unlock()

	w := &writer{
		cache:   c,
		vars:    vars,
		errs:    newErrIndex(errs),
		touched: map[string]bool{},
	}
	w.writeSelectionSet(RootKey, query, data, nil)

	keys := make([]string, 0, len(w.touched))
	for k := range w.touched {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return WriteResult{Entities: keys}
}

type writer struct {
	cache   *Cache
	vars    map[string]any
	errs    *errIndex
	touched map[string]bool
}

func (w *writer) record(key string) *Record {
	rec, ok := w.cache.records[key]
	if !ok {
		rec = &Record{Fields: map[string]any{}}
		w.cache.records[key] = rec
	}
	if rec.Fields == nil {
		rec.Fields = map[string]any{}
	}
	w.touched[key] = true
	return rec
}

// writeSelectionSet 把 data 按选择集写入 key 对应的记录。
func (w *writer) writeSelectionSet(key string, set SelectionSet, data map[string]any, path []any) {
	rec := w.record(key)
	if tn, _ := data[w.cache.opts.TypenameField].(string); tn != "" {
		rec.TypeName = tn
	}
	for _, sel := range set.Selections {
		switch {
		case sel.Field != nil:
			w.writeField(rec, sel.Field, data, path)
		case sel.Fragment != nil:
			// 类型条件匹配（或数据未声明类型）时应用片段
			tn, _ := data[w.cache.opts.TypenameField].(string)
			if tn == "" || tn == sel.Fragment.TypeCondition {
				w.writeSelectionSet(key, sel.Fragment.Selection, data, path)
			}
		}
	}
}

func (w *writer) writeField(rec *Record, f *Field, data map[string]any, path []any) {
	respName := f.ResponseName()
	fieldPath := append(append([]any{}, path...), respName)
	storeKey := FieldKey(f, w.vars)

	// 该字段在响应中带错误：不写入值，记录 Errored 标记。
	if msg, bad := w.errs.at(fieldPath); bad {
		rec.Fields[storeKey] = Errored{Field: storeKey, Message: msg}
		return
	}
	raw, present := data[respName]
	if !present {
		// 响应未返回该字段（例如被 @skip 或服务端裁剪）：不触碰已有值。
		return
	}
	rec.Fields[storeKey] = w.normalizeValue(f.Selection, raw, fieldPath)
}

// normalizeValue 把响应值转换为存储值：实体变为 Reference，列表递归。
func (w *writer) normalizeValue(set SelectionSet, raw any, path []any) any {
	switch v := raw.(type) {
	case map[string]any:
		if key := entityKey(v, w.cache.opts); key != "" {
			w.writeSelectionSet(key, set, v, path)
			return Reference{Key: key}
		}
		// 无身份的内嵌对象：递归归一化其中的实体，整体内联存储。
		out := map[string]any{}
		for k, val := range v {
			out[k] = val
		}
		w.normalizeEmbedded(set, v, out, path)
		return out
	case []any:
		out := make([]any, len(v))
		for i, e := range v {
			out[i] = w.normalizeValue(set, e, append(append([]any{}, path...), i))
		}
		return out
	default:
		return raw
	}
}

// normalizeEmbedded 处理无身份对象：按选择集把其中的实体字段替换为引用。
func (w *writer) normalizeEmbedded(set SelectionSet, src, dst map[string]any, path []any) {
	for _, sel := range set.Selections {
		switch {
		case sel.Field != nil:
			f := sel.Field
			respName := f.ResponseName()
			raw, ok := src[respName]
			if !ok {
				continue
			}
			dst[respName] = w.normalizeValue(f.Selection, raw, append(append([]any{}, path...), respName))
		case sel.Fragment != nil:
			tn, _ := src[w.cache.opts.TypenameField].(string)
			if tn == "" || tn == sel.Fragment.TypeCondition {
				w.normalizeEmbedded(sel.Fragment.Selection, src, dst, path)
			}
		}
	}
}
