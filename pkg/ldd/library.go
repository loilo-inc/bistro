package ldd

type Library struct {
	Name string
	Path string
	Deps []*Library
}

func (l *Library) IsBroken() bool {
	return l.Path == ""
}

func FlattenDeps(libs ...*Library) ([]*Library, []string) {
	var result []*Library
	var broken []string
	visited := make(map[string]bool)
	visitedBroken := make(map[string]bool)
	var queue []*Library
	queue = append(queue, libs...)
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current == nil {
			continue
		}
		if current.IsBroken() {
			if !visitedBroken[current.Name] {
				broken = append(broken, current.Name)
				visitedBroken[current.Name] = true
			}
			continue
		}
		if visited[current.Path] {
			continue
		}
		visited[current.Path] = true
		result = append(result, current)
		queue = append(queue, current.Deps...)
	}
	return result, broken
}
