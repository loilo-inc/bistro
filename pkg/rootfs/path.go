package rootfs

import (
	"path"
)

type Path struct {
	value string
}

func NewPath(value string) Path {
	if path.IsAbs(value) {
		value = path.Join("./", value)
	}
	return Path{value: value}
}

func (p Path) Path() string {
	return p.value
}

func (p Path) Dir() Path {
	return Path{value: path.Dir(p.value)}
}

func (p Path) Join(elem string) Path {
	return Path{value: path.Join(p.value, elem)}
}

func (p Path) Abs() string {
	return path.Join("/", p.value)
}

func (p Path) TarName() string {
	v := p.Abs()[1:]
	if v == "" {
		return "."
	}
	return v
}
