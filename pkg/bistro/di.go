package bistro

import (
	"io/fs"

	"github.com/loilo-inc/bistro/pkg/dpkg"
	"github.com/loilo-inc/bistro/pkg/ldd"
	"github.com/loilo-inc/bistro/pkg/rootfs"
)

type DI struct {
	Logger  Logger
	Ldd     ldd.Resolver
	Query   dpkg.Query
	SymLink rootfs.SymLinkResolver
	FS      fs.FS
}
