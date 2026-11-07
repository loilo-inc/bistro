package bistro

import (
	"testing"

	"github.com/loilo-inc/bistro/pkg/rootfs"
	"github.com/stretchr/testify/assert"
)

func TestIsExcluded(t *testing.T) {
	for _, tc := range []struct {
		name     string
		target   string
		excludes []string
		want     bool
	}{
		{name: "no exclusions", target: "/app/cache/tool"},
		{name: "exact match", target: "/app/cache", excludes: []string{"/app/cache"}, want: true},
		{name: "descendant", target: "/app/cache/nested/tool", excludes: []string{"/app/cache"}, want: true},
		{name: "sibling directory", target: "/app/cache-old/tool", excludes: []string{"/app/cache"}},
		{name: "sibling file", target: "/app/cache.txt", excludes: []string{"/app/cache"}},
		{name: "ancestor", target: "/app", excludes: []string{"/app/cache"}},
		{name: "trailing slash", target: "/app/cache/tool", excludes: []string{"/app/cache/"}, want: true},
		{name: "root descendant", target: "/app/tool", excludes: []string{"/"}, want: true},
		{name: "root exact match", target: "/", excludes: []string{"/"}, want: true},
		{name: "multiple exclusions", target: "/app/cache/tool", excludes: []string{"/other", "/app/cache"}, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, IsExcluded(rootfs.MustPath(tc.target), rootfs.MustPaths(tc.excludes...)))
		})
	}
}
