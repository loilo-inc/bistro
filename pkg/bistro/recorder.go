package bistro

import (
	"io"
	"os"
	"os/exec"

	"github.com/loilo-inc/bistro/pkg/dpkg"
	"github.com/loilo-inc/bistro/pkg/ldd"
)

type Recorder interface {
	RecordSubcommand(dst io.Writer, cmdName string, cmdArgs ...string) ([]*dpkg.Package, error)
}

type recorder struct {
	dpkg dpkg.Dpkg
	ldd  ldd.Ldd
}

var _ Recorder = (*recorder)(nil)

func CreateRecorder(dpkg dpkg.Dpkg, ldd ldd.Ldd) Recorder {
	return &recorder{
		dpkg: dpkg,
		ldd:  ldd,
	}
}

func (r *recorder) RecordSubcommand(dst io.Writer, cmdName string, cmdArgs ...string) ([]*dpkg.Package, error) {
	dpkgListBefore, err := r.dpkg.ListPackages()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(cmdName, cmdArgs...)
	// pipe the command output to the standard output
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err = cmd.Run(); err != nil {
		return nil, err
	}
	dpkgListAfter, err := r.dpkg.ListPackages()
	if err != nil {
		return nil, err
	}
	added, _, err := diffPackages(dpkgListBefore, dpkgListAfter)
	if err != nil {
		return nil, err
	}

	return added, nil
}

func diffPackages(before []*dpkg.Package, after []*dpkg.Package) (
	added []*dpkg.Package, removed []*dpkg.Package, err error,
) {
	beforeList := before
	afterList := after
	beforeMap := make(map[string]*dpkg.Package)
	for _, pkg := range beforeList {
		beforeMap[pkg.Package] = pkg
	}
	afterMap := make(map[string]*dpkg.Package)
	for _, pkg := range afterList {
		afterMap[pkg.Package] = pkg
	}
	for pkgName, pkg := range afterMap {
		if _, ok := beforeMap[pkgName]; !ok {
			added = append(added, pkg)
		}
	}
	for pkgName, pkg := range beforeMap {
		if _, ok := afterMap[pkgName]; !ok {
			removed = append(removed, pkg)
		}
	}
	return added, removed, nil
}
