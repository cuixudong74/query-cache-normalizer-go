package normcache

import (
	"fmt"
	"sort"
	"strings"
)

// resolveVars 递归地把 VarRef 替换为变量值。未提供的变量保持 VarRef，
// 以便错误信息可定位。
func resolveVars(v any, vars map[string]any) any {
	switch t := v.(type) {
	case VarRef:
		if val, ok := vars[t.Name]; ok {
			return val
		}
		return t
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = resolveVars(e, vars)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = resolveVars(e, vars)
		}
		return out
	default:
		return v
	}
}

// canonical 把值序列化为确定性字符串（map 键排序），用于字段键。
func canonical(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		return fmt.Sprintf("%q", t)
	case bool:
		if t {
			return "true"
		}
		return "false"
	case int:
		return fmt.Sprintf("%d", t)
	case int64:
		return fmt.Sprintf("%d", t)
	case float64:
		return fmt.Sprintf("%g", t)
	case VarRef:
		return "$" + t.Name
	case []any:
		parts := make([]string, len(t))
		for i, e := range t {
			parts[i] = canonical(e)
		}
		return "[" + strings.Join(parts, ",") + "]"
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(t))
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%q:%s", k, canonical(t[k])))
		}
		return "{" + strings.Join(parts, ",") + "}"
	default:
		return fmt.Sprintf("%#v", t)
	}
}

// FieldKey 生成实体记录内的字段存储键：字段名 + 规范化参数。
// 同一字段在相同解析后参数下键相同，与参数书写顺序、变量名无关。
func FieldKey(f *Field, vars map[string]any) string {
	if len(f.Args) == 0 {
		return f.Name
	}
	resolved := resolveVars(f.Args, vars)
	return f.Name + "(" + canonical(resolved) + ")"
}
