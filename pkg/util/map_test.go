package util_test

import (
	"testing"

	"github.com/loilo-inc/bistro/pkg/util"
	"github.com/stretchr/testify/require"
)

func TestMapZeroValue(t *testing.T) {
	var m util.Map[string, int]
	require.Zero(t, m.Size())
	require.Zero(t, m.Get("missing"))
	value, ok := m.LookUp("missing")
	require.Zero(t, value)
	require.False(t, ok)
	require.False(t, m.Has("missing"))

	m.Delete("missing")
	m.Clear()
	require.Zero(t, m.Size())
	m.Set("key", 42)
	require.Equal(t, 42, m.Get("key"))
}

func TestMapSetAndLookUp(t *testing.T) {
	var m util.Map[string, int]
	m.Set("first", 42)
	m.Set("zero", 0)
	require.Equal(t, 2, m.Size())

	for key, want := range map[string]int{"first": 42, "zero": 0} {
		value, ok := m.LookUp(key)
		require.True(t, ok, key)
		require.Equal(t, want, value)
		require.Equal(t, want, m.Get(key))
		require.True(t, m.Has(key))
	}
	value, ok := m.LookUp("missing")
	require.Zero(t, value)
	require.False(t, ok)
	require.Zero(t, m.Get("missing"))
	require.False(t, m.Has("missing"))

	m.Set("first", 99)
	require.Equal(t, 99, m.Get("first"))
	require.Equal(t, 2, m.Size())
	require.Zero(t, m.Get("zero"))
}

func TestMapDelete(t *testing.T) {
	var m util.Map[int, string]
	m.Set(1, "first")
	m.Set(2, "second")
	m.Delete(1)
	require.Equal(t, 1, m.Size())
	require.False(t, m.Has(1))
	value, ok := m.LookUp(1)
	require.Empty(t, value)
	require.False(t, ok)
	require.Equal(t, "second", m.Get(2))

	m.Delete(1)
	m.Delete(3)
	require.Equal(t, 1, m.Size())
	m.Delete(2)
	require.Zero(t, m.Size())
	m.Set(1, "again")
	require.Equal(t, "again", m.Get(1))
}

func TestMapClear(t *testing.T) {
	var m util.Map[string, *int]
	value := 42
	m.Set("first", &value)
	m.Set("nil", nil)
	got, ok := m.LookUp("nil")
	require.Nil(t, got)
	require.True(t, ok)
	require.True(t, m.Has("nil"))

	m.Clear()
	require.Zero(t, m.Size())
	for _, key := range []string{"first", "nil"} {
		got, ok := m.LookUp(key)
		require.Nil(t, got)
		require.False(t, ok)
		require.False(t, m.Has(key))
	}
	m.Clear()
	m.Set("again", &value)
	require.Equal(t, 1, m.Size())
	require.Same(t, &value, m.Get("again"))
}

func TestMapIterEmpty(t *testing.T) {
	for _, state := range []string{"zero value", "cleared", "deleted"} {
		t.Run(state, func(t *testing.T) {
			var m util.Map[string, int]
			switch state {
			case "cleared":
				m.Set("key", 42)
				m.Clear()
			case "deleted":
				m.Set("key", 42)
				m.Delete("key")
			}
			for range m.Iter() {
				t.Fatal("empty map yielded an entry")
			}
		})
	}
}

func TestMapIterEntries(t *testing.T) {
	var m util.Map[string, int]
	m.Set("first", 42)
	m.Set("zero", 0)
	m.Set("first", 99)
	got := make(map[string]int)
	for key, value := range m.Iter() {
		require.NotContains(t, got, key, "each key must be yielded once")
		got[key] = value
	}
	require.Equal(t, map[string]int{"first": 99, "zero": 0}, got)
}

func TestMapIterStopsEarlyAndCanBeReused(t *testing.T) {
	var m util.Map[string, int]
	want := map[string]int{"first": 1, "second": 2, "third": 3}
	for key, value := range want {
		m.Set(key, value)
	}
	seq := m.Iter()
	calls := 0
	for key, value := range seq {
		calls++
		require.Equal(t, want[key], value)
		break
	}
	require.Equal(t, 1, calls)
	got := make(map[string]int)
	for key, value := range seq {
		got[key] = value
	}
	require.Equal(t, want, got)
	require.Equal(t, 3, m.Size())
}
