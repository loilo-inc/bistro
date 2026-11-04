package bistro

import (
	"io/fs"

	"github.com/loilo-inc/bistro/pkg/dpkg"
	"github.com/loilo-inc/bistro/pkg/ldd"
)

type DI struct {
	Logger Logger
	Ldd    ldd.Resolver
	Query  dpkg.Query
	FS     fs.FS
}
