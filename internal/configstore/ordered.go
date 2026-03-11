package configstore

// OMap is a minimal ordered mapping used to preserve YAML key ordering.
type OMap struct {
	keys   []string
	values map[string]any
	index  map[string]int
}

func NewOMap() *OMap {
	return &OMap{
		keys:   []string{},
		values: map[string]any{},
		index:  map[string]int{},
	}
}

func (m *OMap) Keys() []string {
	if m == nil {
		return nil
	}
	out := make([]string, len(m.keys))
	copy(out, m.keys)
	return out
}

func (m *OMap) Get(key string) (any, bool) {
	if m == nil {
		return nil, false
	}
	v, ok := m.values[key]
	return v, ok
}

func (m *OMap) Set(key string, value any) {
	if m == nil {
		return
	}
	if _, ok := m.index[key]; !ok {
		m.index[key] = len(m.keys)
		m.keys = append(m.keys, key)
	}
	m.values[key] = value
}

func (m *OMap) Has(key string) bool {
	_, ok := m.Get(key)
	return ok
}

func (m *OMap) Values() map[string]any {
	if m == nil {
		return nil
	}
	return m.values
}
