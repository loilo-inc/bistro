// Package elftest builds small ELF64 little-endian fixtures for tests.
//
// Fixtures are parsed, never executed, so no compiler or host Linux files are
// needed. Like ld.so, consumers are expected to read program headers: a
// dynamic fixture carries PT_LOAD over the whole file and PT_DYNAMIC, whose
// table points at the string table through DT_STRTAB and DT_STRSZ. Matching
// section headers are kept as well, as in a regularly linked file.
package elftest

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// Dyn is a string-valued dynamic entry such as DT_NEEDED or DT_RUNPATH.
type Dyn struct {
	Tag   elf.DynTag
	Value string
}

// DynString returns a string-valued dynamic entry.
func DynString(tag elf.DynTag, value string) Dyn {
	return Dyn{Tag: tag, Value: value}
}

// Needed returns a DT_NEEDED entry for each name.
func Needed(names ...string) []Dyn {
	var entries []Dyn
	for _, name := range names {
		entries = append(entries, DynString(elf.DT_NEEDED, name))
	}
	return entries
}

type Fixture struct {
	// Type defaults to ET_DYN and Machine to EM_X86_64.
	Type    elf.Type
	Machine elf.Machine
	// Interp adds a PT_INTERP segment when set.
	Interp string
	// Static omits PT_LOAD, PT_DYNAMIC and the section headers.
	Static bool
	// Entries are the string-valued dynamic entries, in order.
	Entries []Dyn
	// Extra entries are appended after the generated DT_STRTAB and DT_STRSZ,
	// so they override them.
	Extra []elf.Dyn64
	// Omit drops generated DT_STRTAB or DT_STRSZ entries.
	Omit []elf.DynTag
	// Header, Progs and Sections adjust the headers right before encoding.
	Header   func(*elf.Header64)
	Progs    func([]elf.Prog64)
	Sections func([]elf.Section64)
}

// Bytes encodes the fixture as:
// ELF header | program headers | interpreter | strtab | dynamic | section headers.
func (f Fixture) Bytes() []byte {
	typ, machine := f.Type, f.Machine
	if typ == elf.ET_NONE {
		typ = elf.ET_DYN
	}
	if machine == elf.EM_NONE {
		machine = elf.EM_X86_64
	}
	var progs []elf.Prog64
	if f.Interp != "" {
		progs = append(progs, elf.Prog64{Type: uint32(elf.PT_INTERP)})
	}
	if !f.Static {
		progs = append(progs, elf.Prog64{Type: uint32(elf.PT_LOAD)}, elf.Prog64{Type: uint32(elf.PT_DYNAMIC)})
	}

	interp := []byte(f.Interp)
	if f.Interp != "" {
		interp = append(interp, 0)
	}
	interpOffset := uint64(64 + 56*len(progs))
	strOffset := interpOffset + uint64(len(interp))
	strtab := []byte{0}
	var dynamic []elf.Dyn64
	for _, entry := range f.Entries {
		dynamic = append(dynamic, elf.Dyn64{Tag: int64(entry.Tag), Val: uint64(len(strtab))})
		strtab = append(strtab, entry.Value...)
		strtab = append(strtab, 0)
	}
	for _, generated := range []elf.Dyn64{
		{Tag: int64(elf.DT_STRTAB), Val: strOffset},
		{Tag: int64(elf.DT_STRSZ), Val: uint64(len(strtab))},
	} {
		if !slices.Contains(f.Omit, elf.DynTag(generated.Tag)) {
			dynamic = append(dynamic, generated)
		}
	}
	dynamic = append(dynamic, f.Extra...)
	dynamic = append(dynamic, elf.Dyn64{})
	if f.Static {
		strtab, dynamic = nil, nil
	}
	dynOffset := strOffset + uint64(len(strtab))
	dynSize := uint64(16 * len(dynamic))
	shOffset := dynOffset + dynSize

	for i := range progs {
		p := &progs[i]
		switch elf.ProgType(p.Type) {
		case elf.PT_INTERP:
			p.Off, p.Vaddr = interpOffset, interpOffset
			p.Filesz, p.Memsz = uint64(len(interp)), uint64(len(interp))
		case elf.PT_LOAD:
			p.Filesz, p.Memsz = shOffset, shOffset
		case elf.PT_DYNAMIC:
			p.Off, p.Vaddr = dynOffset, dynOffset
			p.Filesz, p.Memsz = dynSize, dynSize
		}
	}
	var sections []elf.Section64
	if !f.Static {
		sections = []elf.Section64{
			{},
			{Type: uint32(elf.SHT_STRTAB), Off: strOffset, Size: uint64(len(strtab))},
			{Type: uint32(elf.SHT_DYNAMIC), Off: dynOffset, Size: dynSize, Link: 1, Entsize: 16},
		}
	}

	header := elf.Header64{Type: uint16(typ), Machine: uint16(machine), Version: uint32(elf.EV_CURRENT), Ehsize: 64}
	copy(header.Ident[:], []byte{0x7f, 'E', 'L', 'F', byte(elf.ELFCLASS64), byte(elf.ELFDATA2LSB), byte(elf.EV_CURRENT)})
	if len(progs) > 0 {
		header.Phoff, header.Phentsize, header.Phnum = 64, 56, uint16(len(progs))
	}
	if len(sections) > 0 {
		header.Shoff, header.Shentsize, header.Shnum = shOffset, 64, uint16(len(sections))
	}
	if f.Header != nil {
		f.Header(&header)
	}
	if f.Progs != nil {
		f.Progs(progs)
	}
	if f.Sections != nil {
		f.Sections(sections)
	}

	var out bytes.Buffer
	order := binary.LittleEndian
	// Writes to a bytes.Buffer of fixed-size values cannot fail.
	_ = binary.Write(&out, order, header)
	_ = binary.Write(&out, order, progs)
	out.Write(interp)
	out.Write(strtab)
	_ = binary.Write(&out, order, dynamic)
	_ = binary.Write(&out, order, sections)
	return out.Bytes()
}

// Prog returns the first program header of the given type, or nil.
func Prog(progs []elf.Prog64, typ elf.ProgType) *elf.Prog64 {
	for i := range progs {
		if elf.ProgType(progs[i].Type) == typ {
			return &progs[i]
		}
	}
	return nil
}

// Write encodes the fixture to path, creating parent directories.
func Write(t testing.TB, path string, f Fixture) {
	t.Helper()
	writeFile(t, path, f.Bytes())
}

// WriteDynamic writes a default dynamic fixture with the given entries.
func WriteDynamic(t testing.TB, path string, entries ...Dyn) {
	t.Helper()
	Write(t, path, Fixture{Entries: entries})
}

// WriteNotELF writes a file that is too short to hold an ELF identifier.
func WriteNotELF(t testing.TB, path string) {
	t.Helper()
	writeFile(t, path, []byte("not ELF"))
}

func writeFile(t testing.TB, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
}
