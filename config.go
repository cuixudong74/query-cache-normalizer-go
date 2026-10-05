package qcache

// Config controls normalization identity and determinism.
type Config struct {
	// IDFields maps a typename to the field (or fields) that identify it.
	// The "" typename key supplies the global default. The built-in default
	// is the single field "id".
	IDFields map[string][]string
	// DataIDFrom, when non-nil, overrides identity derivation. Returning
	// ("", false) means the object is embedded rather than normalized.
	DataIDFrom func(typename string, data map[string]any) (id string, ok bool)
	// PersistPath, when non-empty, enables Save/Load to this file.
	PersistPath string
}

// defaultIDFields is the fallback identity field.
var defaultIDFields = []string{"id"}

func defaultConfig() Config {
	return Config{
		IDFields: map[string][]string{"": defaultIDFields},
	}
}

// idFieldsFor returns the configured candidate identity fields for a typename.
func (c Config) idFieldsFor(typename string) []string {
	if fs, ok := c.IDFields[typename]; ok && len(fs) > 0 {
		return fs
	}
	if fs, ok := c.IDFields[""]; ok && len(fs) > 0 {
		return fs
	}
	return defaultIDFields
}

// identityOf derives the cache key for an object. Returns ("", false) when the
// object has no usable identity and must be embedded in place. The key form is
// "Typename:joinedIdParts" so equal ids on different types never collide.
func (c Config) identityOf(typename string, data map[string]any) (string, bool) {
	if c.DataIDFrom != nil {
		id, ok := c.DataIDFrom(typename, data)
		if !ok || id == "" {
			return "", false
		}
		return typename + ":" + id, true
	}
	parts := c.idFieldsFor(typename)
	vals := make([]any, 0, len(parts))
	for _, f := range parts {
		v, ok := data[f]
		if !ok || v == nil {
			return "", false
		}
		vals = append(vals, v)
	}
	id := canonicalJSON(vals)
	if len(vals) == 1 {
		id = canonicalJSON(vals[0])
	}
	return typename + ":" + id, true
}
