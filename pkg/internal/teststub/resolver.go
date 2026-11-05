package teststub

import (
	"testing"

	"github.com/loilo-inc/bistro/pkg/ldd"
	"github.com/loilo-inc/bistro/pkg/rootfs"
)

type Resolver struct {
	t               testing.TB
	Loads           int
	LoadErr         error
	ListObjectsFunc func(rootfs.Path) ([]*ldd.Library, error)
}

var _ ldd.Resolver = (*Resolver)(nil)

func NewResolver(t testing.TB) *Resolver {
	return &Resolver{t: t}
}

func (r *Resolver) Load() error {
	r.Loads++
	return r.LoadErr
}

func (r *Resolver) ListObjects(path rootfs.Path) ([]*ldd.Library, error) {
	r.t.Helper()
	if r.ListObjectsFunc == nil {
		r.t.Fatalf("unexpected ELF dependency scan: %s", path)
	}
	return r.ListObjectsFunc(path)
}
