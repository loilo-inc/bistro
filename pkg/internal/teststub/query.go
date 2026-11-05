// Package teststub provides configurable dependency stubs for tests.
package teststub

import (
	"testing"

	"github.com/loilo-inc/bistro/pkg/dpkg"
)

type Query struct {
	t          testing.TB
	Loads      int
	LoadErr    error
	GetFunc    func(string) (*dpkg.Package, error)
	LookUpFunc func(string) (*dpkg.Package, error)
	ListFunc   func() ([]*dpkg.Package, error)
}

var _ dpkg.Query = (*Query)(nil)

func NewQuery(t testing.TB) *Query {
	return &Query{t: t}
}

func (q *Query) Load() error {
	q.Loads++
	return q.LoadErr
}

func (q *Query) Get(name string) (*dpkg.Package, error) {
	q.t.Helper()
	if q.GetFunc == nil {
		q.t.Fatalf("unexpected package lookup: %s", name)
	}
	return q.GetFunc(name)
}

func (q *Query) LookUp(name string) (*dpkg.Package, error) {
	q.t.Helper()
	if q.LookUpFunc == nil {
		q.t.Fatalf("unexpected file owner lookup: %s", name)
	}
	return q.LookUpFunc(name)
}

func (q *Query) List() ([]*dpkg.Package, error) {
	q.t.Helper()
	if q.ListFunc == nil {
		q.t.Fatal("unexpected package listing")
	}
	return q.ListFunc()
}
