package normcache

// SelectionSet 是一组字段/片段选择。
type SelectionSet struct {
	Selections []Selection `json:"selections,omitempty"`
}

// Selection 是单个选择：字段或内联片段（联合类型用）。
type Selection struct {
	Field    *Field          `json:"field,omitempty"`
	Fragment *InlineFragment `json:"fragment,omitempty"`
}

// Field 是一个字段选择，含别名与已解析参数。
type Field struct {
	Name      string         `json:"name"`
	Alias     string         `json:"alias,omitempty"`
	Args      map[string]any `json:"args,omitempty"`
	Selection SelectionSet   `json:"selection,omitempty"`
}

// InlineFragment 是带类型条件的片段，用于联合/接口类型。
type InlineFragment struct {
	TypeCondition string       `json:"typeCondition"`
	Selection     SelectionSet `json:"selection"`
}

// ResponseName 返回字段在响应数据中的键名（别名优先）。
func (f *Field) ResponseName() string {
	if f.Alias != "" {
		return f.Alias
	}
	return f.Name
}

// ParseQuery 解析查询文本为 AST。
func ParseQuery(src string) (SelectionSet, error) {
	return parseQuery(src)
}
