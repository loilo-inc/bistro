package bistro

import (
	"strings"

	"github.com/loilo-inc/bistro/pkg/rootfs"
)

func IsExcluded(path rootfs.Path, excludes []rootfs.Path) bool {
	target := path.Abs()
	for _, prefix := range excludes {
		excluded := prefix.Abs()
		if target == excluded ||
			strings.HasPrefix(target, strings.TrimSuffix(excluded, "/")+"/") {
			return true
		}
	}
	return false
}
