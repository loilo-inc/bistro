package main

import (
	"debug/elf"
	"errors"
	"os"

	"github.com/loilo-inc/bistro/pkg/ldd"
)

func main() {
	resolver, err := ldd.DefaultResolver()
	if err != nil {
		panic(err)
	}
	if err := resolver.Load(); err != nil {
		panic(err)
	}

	extCode := 0
	for _, arg := range os.Args[1:] {
		libs, err := resolver.ListObjects(arg)
		if _, ok := errors.AsType[*elf.FormatError](err); ok {
			println("not an ELF file:", arg)
			continue
		}
		if err != nil {
			panic(err)
		}
		var visited = make(map[*ldd.Library]struct{})
		var uniqueLibs []*ldd.Library
		var queue []*ldd.Library
		queue = append(queue, libs...)

		for len(queue) > 0 {
			lib := queue[0]
			queue = queue[1:]
			if _, ok := visited[lib]; ok {
				continue
			}
			visited[lib] = struct{}{}
			uniqueLibs = append(uniqueLibs, lib)
			queue = append(queue, lib.Deps...)
		}
		println(arg + ":")
		for _, lib := range uniqueLibs {
			println("\t" + lib.Name + " => " + lib.Path)
		}
	}
	os.Exit(extCode)
}
