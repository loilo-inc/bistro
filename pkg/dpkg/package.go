package dpkg

import (
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/loilo-inc/bistro/pkg/rootfs"
)

// https://man7.org/linux/man-pages/man1/dpkg-query.1.html
type Package struct {
	baseDir   string
	fields    []*Field
	fieldsMap map[string]*Field
	list      *ListData
	md5Sums   *Md5Sums
}

type Field struct {
	Key   string
	Value string
}

type ListData struct {
	Files []rootfs.Path
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

func (p *Package) field(key string) string {
	if f, ok := p.fieldsMap[key]; ok {
		return f.Value
	}
	return ""
}

func (p *Package) Name() string {
	return p.field("Package")
}

// NameWithArch returns the package name used for dpkg info files.
// Only Multi-Arch: same packages use architecture-qualified filenames.
func (p *Package) NameWithArch() string {
	if p.field("Multi-Arch") == "same" {
		return p.Name() + ":" + p.Architecture()
	}
	return p.Name()
}

func (p *Package) Version() string {
	return p.field("Version")
}

type Status struct {
	Wanted  string
	Eflag   string
	Current string
}

func (p *Package) Status() Status {
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

func (p *Package) Architecture() string {
	return p.field("Architecture")
}

func (p *Package) infoPath(extension string) string {
	baseDir := p.baseDir
	if baseDir == "" {
		baseDir = DefaultBaseDir
	}
	return path.Join(baseDir, "info", p.NameWithArch()+extension)
}

func (p *Package) ListFiles() ([]rootfs.Path, error) {
	if p.list == nil {
		listFile := p.infoPath(".list")
		content, err := os.ReadFile(listFile)
		// ignore non-exist error for various reasons (e.g., file removed)
		if os.IsNotExist(err) {
			p.list = &ListData{Files: []rootfs.Path{}}
			return p.list.Files, nil
		}
		if err != nil {
			return nil, err
		}
		var files []rootfs.Path
		for f := range strings.SplitSeq(string(content), "\n") {
			if f == "" {
				continue
			}
			files = append(files, rootfs.NewPath(f))
		}
		p.list = &ListData{Files: files}
	}
	return p.list.Files, nil
}

func (p *Package) Md5Sums() (*Md5Sums, error) {
	if p.md5Sums == nil {
		md5File := p.infoPath(".md5sums")
		content, err := os.ReadFile(md5File)
		// .md5sums file might not exist
		if os.IsNotExist(err) {
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

func (p *Package) String() string {
	var sb strings.Builder
	for _, field := range p.fields {
		fmt.Fprintf(&sb, "%s: %s\n", field.Key, field.Value)
	}
	return sb.String()
}
