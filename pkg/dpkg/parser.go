package dpkg

import (
	"fmt"
	"regexp"
	"strings"
)

var attrRegexp = regexp.MustCompile(`^([A-Za-z0-9-]+):\s*(.*)$`)

func ParseStatus(statusContent string) ([]*Package, error) {
	var current *Package
	var currentField *Field
	var packages []*Package
	for line := range strings.SplitSeq(statusContent, "\n") {
		if line == "" {
			if current != nil {
				packages = append(packages, current)
				current = nil
				currentField = nil
			}
			continue
		}
		if current == nil {
			current = &Package{
				fieldsMap: make(map[string]*Field),
			}
		}
		if matches := attrRegexp.FindStringSubmatch(line); matches != nil {
			key := matches[1]
			value := matches[2]
			field := &Field{
				Key:   key,
				Value: value,
			}
			current.fields = append(current.fields, field)
			current.fieldsMap[key] = field
			currentField = field
		} else if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			// Continuation line for the previous key
			if current != nil && currentField != nil {
				// keep indentation for continuation lines
				currentField.Value += "\n" + line
			}
		} else {
			return nil, fmt.Errorf("invalid line in status file: %s", line)
		}
	}
	if current != nil {
		packages = append(packages, current)
	}
	return packages, nil
}
