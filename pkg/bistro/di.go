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

func NewDI(logger Logger, ldd ldd.Resolver, query dpkg.Query, fs fs.FS) DI {
	return DI{
		Logger: logger,
		Ldd:    ldd,
		Query:  query,
		FS:     fs,
	}
}
