package normcache

import "sync"

// Record 是一个实体的归一化记录。
type Record struct {
	TypeName string         `json:"typeName,omitempty"`
	Fields   map[string]any `json:"fields,omitempty"`
}

// Options 是缓存配置项。零值即默认配置。
type Options struct {
	// IDField 是实体身份字段名，默认 "id"。
	IDField string
	// TypenameField 是实体类型字段名，默认 "__typename"。
	TypenameField string
}

func (o Options) withDefaults() Options {
	if o.IDField == "" {
		o.IDField = "id"
	}
	if o.TypenameField == "" {
		o.TypenameField = "__typename"
	}
	return o
}

// Cache 是归一化缓存，支持并发读写（读获取一致快照）。
type Cache struct {
	mu      sync.RWMutex
	records map[string]*Record
	opts    Options
}

// New 以默认配置创建空缓存。
func New() *Cache {
	return NewWithOptions(Options{})
}

// NewWithOptions 以指定配置创建空缓存。
func NewWithOptions(opts Options) *Cache {
	return &Cache{records: map[string]*Record{}, opts: opts.withDefaults()}
}
