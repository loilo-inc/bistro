package elftest

import (
	"bytes"
	"debug/elf"
	"io"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func parse(t *testing.T, f Fixture) (*elf.File, []byte) {
	t.Helper()
	data := f.Bytes()
	file, err := elf.NewFile(bytes.NewReader(data))
	require.NoError(t, err)
	return file, data
}

func TestDynamicFixture(t *testing.T) {
	file, data := parse(t, Fixture{Entries: append(Needed("libone.so", "libtwo.so"),
		DynString(elf.DT_SONAME, "libfoo.so"),
		DynString(elf.DT_RUNPATH, ""),
	)})
	require.Equal(t, elf.ET_DYN, file.Type)
	require.Equal(t, elf.EM_X86_64, file.Machine)
	require.Equal(t, elf.ELFCLASS64, file.Class)
	require.Equal(t, elf.ELFDATA2LSB, file.Data)
	require.Len(t, file.Progs, 2)
	load, dynamic := file.Progs[0], file.Progs[1]
	require.Equal(t, elf.PT_LOAD, load.Type)
	require.Equal(t, uint64(0), load.Off)
	require.Equal(t, uint64(len(data))-3*64, load.Filesz, "PT_LOAD covers everything before the section headers")
	require.Equal(t, elf.PT_DYNAMIC, dynamic.Type)
	require.Equal(t, dynamic.Off, dynamic.Vaddr)

	// Section headers describe the same table as the program headers.
	needed, err := file.DynString(elf.DT_NEEDED)
	require.NoError(t, err)
	require.Equal(t, []string{"libone.so", "libtwo.so"}, needed)
	soname, err := file.DynString(elf.DT_SONAME)
	require.NoError(t, err)
	require.Equal(t, []string{"libfoo.so"}, soname)
	runpath, err := file.DynString(elf.DT_RUNPATH)
	require.NoError(t, err)
	require.Equal(t, []string{""}, runpath, "empty strings are preserved")

	strtab, err := file.DynValue(elf.DT_STRTAB)
	require.NoError(t, err)
	require.Len(t, strtab, 1)
	strsz, err := file.DynValue(elf.DT_STRSZ)
	require.NoError(t, err)
	require.Len(t, strsz, 1)
	str := file.SectionByType(elf.SHT_STRTAB)
	require.Equal(t, str.Offset, strtab[0], "DT_STRTAB is a vaddr equal to the file offset")
	require.Equal(t, str.Size, strsz[0])
}

func TestInterpAndType(t *testing.T) {
	file, _ := parse(t, Fixture{Type: elf.ET_EXEC, Machine: elf.EM_AARCH64, Interp: "/lib/ld.so"})
	require.Equal(t, elf.ET_EXEC, file.Type)
	require.Equal(t, elf.EM_AARCH64, file.Machine)
	require.Len(t, file.Progs, 3)
	interp := file.Progs[0]
	require.Equal(t, elf.PT_INTERP, interp.Type)
	got, err := io.ReadAll(interp.Open())
	require.NoError(t, err)
	require.Equal(t, "/lib/ld.so\x00", string(got))
}

func TestStaticFixture(t *testing.T) {
	file, data := parse(t, Fixture{Static: true, Entries: Needed("ignored.so")})
	require.Empty(t, file.Progs)
	require.Empty(t, file.Sections)
	require.Len(t, data, 64, "a static fixture is only the ELF header")

	file, _ = parse(t, Fixture{Static: true, Interp: "/lib/ld.so"})
	require.Len(t, file.Progs, 1)
	require.Equal(t, elf.PT_INTERP, file.Progs[0].Type)
}

func TestOmitAndExtra(t *testing.T) {
	file, _ := parse(t, Fixture{
		Omit:  []elf.DynTag{elf.DT_STRTAB},
		Extra: []elf.Dyn64{{Tag: int64(elf.DT_STRSZ), Val: 42}},
	})
	strtab, err := file.DynValue(elf.DT_STRTAB)
	require.NoError(t, err)
	require.Empty(t, strtab)
	strsz, err := file.DynValue(elf.DT_STRSZ)
	require.NoError(t, err)
	require.Equal(t, []uint64{1, 42}, strsz, "extra entries follow the generated ones")
}

func TestMutators(t *testing.T) {
	file, _ := parse(t, Fixture{
		Header:   func(h *elf.Header64) { h.Ident[elf.EI_OSABI] = byte(elf.ELFOSABI_FREEBSD) },
		Progs:    func(p []elf.Prog64) { Prog(p, elf.PT_DYNAMIC).Flags = uint32(elf.PF_R) },
		Sections: func(s []elf.Section64) { s[2].Size = 1 },
	})
	require.Equal(t, elf.ELFOSABI_FREEBSD, file.OSABI)
	require.Equal(t, elf.PF_R, file.Progs[1].Flags)
	require.Equal(t, uint64(1), file.SectionByType(elf.SHT_DYNAMIC).Size)
}

func TestProg(t *testing.T) {
	progs := []elf.Prog64{{Type: uint32(elf.PT_LOAD)}, {Type: uint32(elf.PT_LOAD), Off: 1}}
	require.Same(t, &progs[0], Prog(progs, elf.PT_LOAD))
	require.Nil(t, Prog(progs, elf.PT_DYNAMIC))
}

func TestWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "lib.so")
	WriteDynamic(t, path, Needed("libone.so")...)
	file, err := elf.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = file.Close() })
	needed, err := file.DynString(elf.DT_NEEDED)
	require.NoError(t, err)
	require.Equal(t, []string{"libone.so"}, needed)

	notELF := filepath.Join(dir, "other", "not.so")
	WriteNotELF(t, notELF)
	_, err = elf.Open(notELF)
	var formatErr *elf.FormatError
	require.ErrorAs(t, err, &formatErr)
}
