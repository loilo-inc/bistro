package bistro

import "strings"

func parseLines(data []byte) []string {
	lines := strings.Split(string(data), "\n")
	var result []string
	for _, line := range lines {
		line = strings.TrimPrefix(line, " ")
		if line == "" {
			continue
		}
		result = append(result, line)
	}
	return result
}

func parseTabSplitData(data []byte) [][]string {
	lines := parseLines(data)
	var result [][]string
	for _, line := range lines {
		result = append(result, strings.Split(line, "\t"))
	}
	return result
}

func parseSearchData(data []byte) []*OwnedFile {
	lines := parseLines(data)
	var result []*OwnedFile
	for _, line := range lines {
		libs := parseSearchLine(line)
		result = append(result, libs...)
	}
	return result
}

func parseSearchLine(line string) []*OwnedFile {
	// libc6:amd64, libc6:i386: /usr/share/doc/libc6
	parts := strings.SplitN(line, ": ", 2)
	if len(parts) < 2 {
		return nil
	}
	var result []*OwnedFile
	libs := strings.Split(parts[0], ",")
	path := strings.TrimSpace(parts[1])
	for i := range libs {
		result = append(result, &OwnedFile{
			Package: strings.TrimSpace(libs[i]),
			File:    path,
		})
	}
	return result
}
