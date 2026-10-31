package bistro

import "github.com/loilo-inc/bistro/pkg/dpkg"

type DI struct {
	Execer Execer
	Logger Logger
	Ldd    Ldd
	Query  dpkg.Query
}
