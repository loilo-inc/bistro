package util

type Map[K comparable, V any] struct {
	data map[K]V
}

func (m *Map[K, V]) Get(key K) V {
	v, _ := m.LookUp(key)
	return v
}

func (m *Map[K, V]) LookUp(key K) (V, bool) {
	if m.data == nil {
		var zero V
		return zero, false
	}
	v, ok := m.data[key]
	return v, ok
}

func (m *Map[K, V]) Clear() {
	if m.data != nil {
		m.data = make(map[K]V)
	}
}

func (m *Map[K, V]) Size() int {
	if m.data == nil {
		return 0
	}
	return len(m.data)
}

func (m *Map[K, V]) Set(key K, value V) {
	if m.data == nil {
		m.data = make(map[K]V)
	}
	m.data[key] = value
}

func (m *Map[K, V]) Delete(key K) {
	if m.data != nil {
		delete(m.data, key)
	}
}

func (m *Map[K, V]) Has(key K) bool {
	if m.data == nil {
		return false
	}
	_, ok := m.data[key]
	return ok
}
