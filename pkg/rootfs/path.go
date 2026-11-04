package rootfs

import (
	"fmt"
	"path"
	"strings"
)

type Path struct {
	value string
}

func NewPath(value string) (Path, error) {
	value = path.Clean(value)
	if value == ".." || strings.HasPrefix(value, "../") {
		return Path{}, fmt.Errorf("path cannot start with '..': %s", value)
	}
	if path.IsAbs(value) {
		value = path.Join("./", value)
	}
	return Path{value: value}, nil
}

func MustPath(value string) Path {
	p, err := NewPath(value)
	if err != nil {
		panic(err)
	}
	return p
}

func Paths(paths ...string) ([]Path, error) {
	var result []Path
	for _, p := range paths {
		pp, err := NewPath(p)
		if err != nil {
			return nil, err
		}
		result = append(result, pp)
	}
	return result, nil
}

func MustPaths(paths ...string) []Path {
	var result []Path
	for _, p := range paths {
		result = append(result, MustPath(p))
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
	return MustPath(path.Dir(p.value))
}

func (p Path) Join(elem string) Path {
	return MustPath(path.Join(p.value, elem))
}

func (p Path) Resolve(target string) Path {
	if path.IsAbs(target) {
		return MustPath(target)
	}
	return MustPath(path.Join(p.Abs(), target))
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
