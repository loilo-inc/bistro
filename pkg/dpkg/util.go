package dpkg

type PackageDiff struct {
	Added   []*Package
	Removed []*Package
	Changed []*Package
}

func DiffPackages(before []*Package, after []*Package) (
	diff PackageDiff, err error,
) {
	beforeList := before
	afterList := after
	var added []*Package
	var removed []*Package
	var changed []*Package
	beforeMap := make(map[string]*Package)
	for _, pkg := range beforeList {
		beforeMap[pkg.Package] = pkg
	}
	afterMap := make(map[string]*Package)
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
	for pkgName, pkg := range afterMap {
		if beforePkg, ok := beforeMap[pkgName]; ok {
			if beforePkg.Version != pkg.Version {
				changed = append(changed, pkg)
			}
		}
	}
	return PackageDiff{
		Added:   added,
		Removed: removed,
		Changed: changed,
	}, nil
}
