package bistro

import (
	"github.com/loilo-inc/bistro/pkg/dpkg"
	"github.com/loilo-inc/bistro/pkg/ldd"
)

type DI struct {
	Logger Logger
	Ldd    ldd.Resolver
	Query  dpkg.Query
}
