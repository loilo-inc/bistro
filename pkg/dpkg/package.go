package dpkg

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"
)

type Package interface {
	Name() string
	NameWithArch() string
	Version() string
	Status() Status
	Architecture() string
	ListFiles() ([]string, error)
	Md5Sums() (*Md5Sums, error)
	String() string
}

// https://man7.org/linux/man-pages/man1/dpkg-query.1.html
type pkg struct {
	fields    []*Field
	fieldsMap map[string]*Field
	list      *ListData
	md5Sums   *Md5Sums
	fs        fs.FS
}

var _ Package = (*pkg)(nil)

type Field struct {
	Key   string
	Value string
}

type ListData struct {
	Files []string
}

type Md5Sums struct {
	Entries []Checksum
}

func (m *Md5Sums) String() string {
	var sb strings.Builder
	for _, entry := range m.Entries {
		fmt.Fprintf(&sb, "%s  %s\n", entry.Sum, entry.File)
	}
	return sb.String()
}

type Checksum struct {
	Sum  string
	File string
}

func (p *pkg) field(key string) string {
	if f, ok := p.fieldsMap[key]; ok {
		return f.Value
	}
	return ""
}

func (p *pkg) Name() string {
	return p.field("Package")
}

// NameWithArch returns the package name used for dpkg info files.
// Only Multi-Arch: same packages use architecture-qualified filenames.
func (p *pkg) NameWithArch() string {
	if p.field("Multi-Arch") == "same" {
		return p.Name() + ":" + p.Architecture()
	}
	return p.Name()
}

func (p *pkg) Version() string {
	return p.field("Version")
}

type Status struct {
	Wanted  string
	Eflag   string
	Current string
}

func (p *pkg) Status() Status {
	// Example: install ok installed
	parts := strings.Split(p.field("Status"), " ")
	if len(parts) != 3 {
		return Status{}
	}
	return Status{
		Wanted:  parts[0],
		Eflag:   parts[1],
		Current: parts[2],
	}
}

func (p *pkg) Architecture() string {
	return p.field("Architecture")
}

func (p *pkg) infoPath(extension string) string {
	return path.Join(DefaultBaseDir, "info", p.NameWithArch()+extension)
}

func (p *pkg) ListFiles() ([]string, error) {
	if p.list == nil {
		listFile := p.infoPath(".list")
		content, err := fs.ReadFile(p.fs, listFile)
		// ignore non-exist error for various reasons (e.g., file removed)
		if errors.Is(err, fs.ErrNotExist) {
			p.list = &ListData{Files: []string{}}
			return p.list.Files, nil
		}
		if err != nil {
			return nil, err
		}
		var files []string
		for f := range strings.SplitSeq(string(content), "\n") {
			if f == "" {
				continue
			}
			files = append(files, f)
		}
		p.list = &ListData{Files: files}
	}
	return p.list.Files, nil
}

func (p *pkg) Md5Sums() (*Md5Sums, error) {
	if p.md5Sums == nil {
		content, err := fs.ReadFile(p.fs, p.infoPath(".md5sums"))
		// .md5sums file might not exist
		if errors.Is(err, fs.ErrNotExist) {
			p.md5Sums = &Md5Sums{Entries: []Checksum{}}
			return p.md5Sums, nil
		} else if err != nil {
			return nil, err
		}
		var checusums []Checksum
		for line := range strings.SplitSeq(string(content), "\n") {
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, "  ", 2)
			if len(parts) != 2 {
				continue
			}
			checusums = append(checusums, Checksum{
				Sum:  parts[0],
				File: parts[1],
			})
		}
		p.md5Sums = &Md5Sums{Entries: checusums}
	}
	return p.md5Sums, nil
}

func (p *pkg) String() string {
	var sb strings.Builder
	for _, field := range p.fields {
		fmt.Fprintf(&sb, "%s: %s\n", field.Key, field.Value)
	}
	return sb.String()
}
