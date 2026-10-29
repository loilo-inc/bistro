package bistro

import (
	"fmt"
	"strings"
)

func NewLdd(execer Execer) Ldd {
	return &ldd{execer: execer}
}

type Ldd interface {
	ListObjects(file string) (*LinkInfo, error)
}

type ldd struct {
	execer Execer
}

func (l *ldd) ListObjects(file string) (*LinkInfo, error) {
	data, err := l.execer.Output("ldd", file)
	if err != nil {
		return nil, err
	}
	return parseLddResult(data)
}

const LddNotFound = "not found"

type LinkInfo struct {
	Ld   string
	Vdso string
	Libs []*Library
}

type Library struct {
	Name string
	Path string
}

func (l *Library) IsBroken() bool {
	return l.Path == ""
}

func parseLddResult(data []byte) (*LinkInfo, error) {
	info := &LinkInfo{}
	lines := strings.SplitSeq(string(data), "\n")
	for line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		/** Example result of ldd
		    linux-vdso.so.1 (0x00007ffd12340000)
		    libfoo.so.1 => /lib/x86_64-linux-gnu/libfoo.so.1 (0x00007f1234000000)
		    libmissing.so.1 => not found
		    /lib64/ld-linux-x86-64.so.2 (0x00007f5678000000)
				**/
		parts := strings.SplitN(line, "=>", 2)
		if len(parts) != 2 {
			if strings.HasPrefix(line, "linux-vdso.so") {
				info.Vdso = line
			} else if strings.HasPrefix(line, "/lib") {
				info.Ld = line
			} else {
				return nil, fmt.Errorf("unexpected line in ldd output: %s", line)
			}
			continue
		}
		lib := strings.TrimSpace(parts[0])
		target := strings.TrimSpace(parts[1])
		if target == LddNotFound {
			info.Libs = append(info.Libs, &Library{
				Name: lib,
				Path: "",
			})
		} else {
			path, _, _ := strings.Cut(target, " ")
			info.Libs = append(info.Libs, &Library{
				Name: lib,
				Path: path,
			})
		}
	}
	return info, nil
}
