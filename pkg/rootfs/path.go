package rootfs

import (
	"path"
	"strings"
)

type Path struct {
	value string
}

func NewPath(value string) Path {
	if path.IsAbs(value) {
		value = path.Join("./", value)
	}
	value = path.Clean(value)
	return Path{value: value}
}

func Paths(paths ...string) []Path {
	var result []Path
	for _, p := range paths {
		result = append(result, NewPath(p))
	}
	return result
}

func (p Path) SplitList(list string) []Path {
	if list == "" {
		return nil
	}
	var result []Path
	for v := range strings.SplitSeq(list, ":") {
		result = append(result, p.Resolve(v))
	}
	return result
}

func (p Path) Path() string {
	return p.value
}

func (p Path) Dir() Path {
	return NewPath(path.Dir(p.value))
}

func (p Path) Join(elem string) Path {
	return NewPath(path.Join(p.value, elem))
}

func (p Path) Resolve(target string) Path {
	if path.IsAbs(target) {
		return NewPath(target)
	}
	return NewPath(path.Join(p.Abs(), target))
}

func (p Path) Abs() string {
	return path.Join("/", p.value)
}

func (p Path) String() string {
	return p.Abs()
}

func (p Path) TarName() string {
	v := p.Abs()[1:]
	if v == "" {
		return "."
	}
	return v
}
