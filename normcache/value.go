package normcache

// Reference 是指向某个实体记录的引用，用于替代内联的嵌套对象。
type Reference struct {
	Key string `json:"key"`
}

// Missing 是可区分的“缓存缺失”标记，绝不用 nil/空值冒充。
type Missing struct {
	Field string `json:"field"`
}

// Errored 是“该字段上次响应带错误被跳过”的标记，与真正缺失区分。
type Errored struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// IsReference 判断值是否为实体引用。
func IsReference(v any) (Reference, bool) {
	r, ok := v.(Reference)
	return r, ok
}
