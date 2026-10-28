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
