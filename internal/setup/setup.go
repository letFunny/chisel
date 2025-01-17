package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/crypto/openpgp/packet"

	"github.com/canonical/chisel/internal/strdist"
)

// Release is a collection of package slices targeting a particular
// distribution version.
type Release struct {
	Path          string
	Packages      map[string]*Package
	Archives      map[string]*Archive
	ConflictRanks map[string]map[string]int
}

// Archive is the location from which binary packages are obtained.
type Archive struct {
	Name       string
	Version    string
	Suites     []string
	Components []string
	Priority   int
	Pro        string
	PubKeys    []*packet.PublicKey
}

// Package holds a collection of slices that represent parts of themselves.
type Package struct {
	Name    string
	Path    string
	Archive string
	Slices  map[string]*Slice
}

// Slice holds the details about a package slice.
type Slice struct {
	Package   string
	Name      string
	Essential []SliceKey
	Contents  map[string]PathInfo
	Scripts   SliceScripts
}

type SliceScripts struct {
	Mutate string
}

type PathKind string

const (
	DirPath      PathKind = "dir"
	CopyPath     PathKind = "copy"
	GlobPath     PathKind = "glob"
	TextPath     PathKind = "text"
	SymlinkPath  PathKind = "symlink"
	GeneratePath PathKind = "generate"

	// TODO Maybe in the future, for binary support.
	//Base64Path PathKind = "base64"
)

type PathUntil string

const (
	UntilNone   PathUntil = ""
	UntilMutate PathUntil = "mutate"
)

type GenerateKind string

const (
	GenerateNone     GenerateKind = ""
	GenerateManifest GenerateKind = "manifest"
)

type PathInfo struct {
	Kind PathKind
	Info string
	Mode uint

	Mutable  bool
	Until    PathUntil
	Arch     []string
	Generate GenerateKind
	Prefer   string
}

// SameContent returns whether the path has the same content properties as some
// other path. In other words, the resulting file/dir entry is the same. The
// Mutable flag must also match, as that's a common agreement that the actual
// content is not well defined upfront.
func (pi *PathInfo) SameContent(other *PathInfo) bool {
	return (pi.Kind == other.Kind &&
		pi.Info == other.Info &&
		pi.Mode == other.Mode &&
		pi.Mutable == other.Mutable &&
		pi.Generate == other.Generate &&
		pi.Prefer == other.Prefer)
}

type SliceKey struct {
	Package string
	Slice   string
}

func (s *Slice) String() string   { return s.Package + "_" + s.Name }
func (s SliceKey) String() string { return s.Package + "_" + s.Slice }

// Selection holds the required configuration to create a Build for a selection
// of slices from a Release. It's still an abstract proposal in the sense that
// the real information coming from packages is still unknown, so referenced
// paths could potentially be missing, for example.
type Selection struct {
	Release *Release
	Slices  []*Slice
}

func ReadRelease(dir string) (*Release, error) {
	logDir := dir
	if strings.Contains(dir, "/.cache/") {
		logDir = filepath.Base(dir)
	}
	logf("Processing %s release...", logDir)

	release := &Release{
		Path:     dir,
		Packages: make(map[string]*Package),
	}

	release, err := readRelease(dir)
	if err != nil {
		return nil, err
	}

	err = release.validate()
	if err != nil {
		return nil, err
	}
	return release, nil
}

func (r *Release) validate() error {
	keys := []SliceKey(nil)

	// Check for info conflicts and prepare for following checks. A conflict
	// means that two slices attempt to extract different files or directories
	// to the same location.
	// Conflict validation is done without downloading packages which means that
	// if we are extracting content from different packages to the same location
	// we cannot be sure that it will be the same. On the contrary, content
	// extracted from the same package will never conflict because it is
	// guaranteed to be the same.
	// The above also means that generated content (e.g. text files, directories
	// with make:true) will always conflict with extracted content, because we
	// cannot validate that they are the same without downloading the package.
	paths := make(map[string]*Slice)
	globs := make(map[string]*Slice)
	conflicts := make(map[string]*conflictGraph)

	// Iterate on a stable package order.
	var pkgNames []string
	for _, pkg := range r.Packages {
		pkgNames = append(pkgNames, pkg.Name)
	}
	slices.Sort(pkgNames)
	for _, pkgName := range pkgNames {
		pkg := r.Packages[pkgName]
		for _, new := range pkg.Slices {
			keys = append(keys, SliceKey{pkg.Name, new.Name})
			for newPath, newInfo := range new.Contents {
				if old, ok := paths[newPath]; ok {
					oldInfo := old.Contents[newPath]
					sameContent := newInfo.SameContent(&oldInfo)
					if newInfo.Kind == CopyPath || newInfo.Kind == GlobPath {
						sameContent = sameContent && (new.Package == old.Package)
					}

					reportConflict := func(old, new *Slice, path string) error {
						if old.Package > new.Package || old.Package == new.Package && old.Name > new.Name {
							old, new = new, old
						}
						return fmt.Errorf("slices %s and %s conflict on %s", old, new, path)
					}

					g := conflicts[newPath]
					if s, ok := g.visited[new.Package]; ok {
						sInfo := s.Contents[newPath]
						if !newInfo.SameContent(&sInfo) {
							return reportConflict(s, new, newPath)
						}
						continue
					}

					if newInfo.Prefer != "" {
						if g.hasNoPrefers() {
							return reportConflict(old, new, newPath)
						}
						if err := g.walk(new.Package, old.Package); err != nil {
							return err
						}
						paths[newPath] = new
					} else {
						if !sameContent {
							return reportConflict(old, new, newPath)
						}
						g.visited[new.Package] = new
					}
				} else {
					paths[newPath] = new
					if newInfo.Kind == GeneratePath || newInfo.Kind == GlobPath {
						globs[newPath] = new
					}
					g := &conflictGraph{
						path:    newPath,
						release: r,
						visited: make(map[string]*Slice),
					}
					conflicts[newPath] = g
					if newInfo.Prefer != "" {
						if err := g.walk(new.Package, ""); err != nil {
							return err
						}
					} else {
						g.visited[new.Package] = new
						g.head = new.Package
					}
				}
			}
		}
	}
	for path, g := range conflicts {
		if !g.isLinear() {
			continue
		}
		if r.ConflictRanks == nil {
			r.ConflictRanks = make(map[string]map[string]int)
		}
		r.ConflictRanks[path] = make(map[string]int)
		for cur, i := g.head, 1; cur != ""; cur, i = g.next(cur), i+1 {
			r.ConflictRanks[path][cur] = i
		}
	}

	// Check for glob and generate conflicts.
	for oldPath, old := range globs {
		oldInfo := old.Contents[oldPath]
		for newPath, new := range paths {
			if oldPath == newPath {
				// Identical globs have been filtered earlier. This must be the
				// exact same entry.
				continue
			}
			newInfo := new.Contents[newPath]
			if oldInfo.Kind == GlobPath && (newInfo.Kind == GlobPath || newInfo.Kind == CopyPath) {
				if new.Package == old.Package && !conflicts[newPath].isLinear() {
					// We do not need to check for a conflict if the new path
					// comes from the same package, is either a glob or a copy
					// path and is not part of any conflict chain.
					continue
				}
			}
			if strdist.GlobPath(newPath, oldPath) {
				if oldInfo.Kind == GlobPath && newInfo.Kind == CopyPath &&
					new.Package == old.Package && conflicts[newPath].isLinear() {
					// In this case, we have found a CopyPath that conflicts
					// with a GlobPath from the same package and the CopyPath is
					// part of a conflict chain. Since the two paths are from
					// the same package, we should find another package in the
					// CopyPath chain to report the error. Since this package is
					// the head of the chain, report the next package in chain.
					next := conflicts[newPath].next(new.Package)
					new = conflicts[newPath].visited[next]
				}
				if (old.Package > new.Package) || (old.Package == new.Package && old.Name > new.Name) ||
					(old.Package == new.Package && old.Name == new.Name && oldPath > newPath) {
					old, new = new, old
					oldPath, newPath = newPath, oldPath
				}
				return fmt.Errorf("slices %s and %s conflict on %s and %s", old, new, oldPath, newPath)
			}
		}
	}

	// Check for cycles.
	_, err := order(r.Packages, keys)
	if err != nil {
		return err
	}

	// Check for archive priority conflicts.
	priorities := make(map[int]*Archive)
	for _, archive := range r.Archives {
		if old, ok := priorities[archive.Priority]; ok {
			if old.Name > archive.Name {
				archive, old = old, archive
			}
			return fmt.Errorf("chisel.yaml: archives %q and %q have the same priority value of %d", old.Name, archive.Name, archive.Priority)
		}
		priorities[archive.Priority] = archive
	}

	// Check that archives pinned in packages are defined.
	for _, pkg := range r.Packages {
		if pkg.Archive == "" {
			continue
		}
		if _, ok := r.Archives[pkg.Archive]; !ok {
			return fmt.Errorf("%s: package refers to undefined archive %q", pkg.Path, pkg.Archive)
		}
	}

	return nil
}

func order(pkgs map[string]*Package, keys []SliceKey) ([]SliceKey, error) {

	// Preprocess the list to improve error messages.
	for _, key := range keys {
		if pkg, ok := pkgs[key.Package]; !ok {
			return nil, fmt.Errorf("slices of package %q not found", key.Package)
		} else if _, ok := pkg.Slices[key.Slice]; !ok {
			return nil, fmt.Errorf("slice %s not found", key)
		}
	}

	// Collect all relevant package slices.
	successors := map[string][]string{}
	pending := append([]SliceKey(nil), keys...)

	seen := make(map[SliceKey]bool)
	for i := 0; i < len(pending); i++ {
		key := pending[i]
		if seen[key] {
			continue
		}
		seen[key] = true
		pkg := pkgs[key.Package]
		slice := pkg.Slices[key.Slice]
		fqslice := slice.String()
		predecessors := successors[fqslice]
		for _, req := range slice.Essential {
			fqreq := req.String()
			if reqpkg, ok := pkgs[req.Package]; !ok || reqpkg.Slices[req.Slice] == nil {
				return nil, fmt.Errorf("%s requires %s, but slice is missing", fqslice, fqreq)
			}
			predecessors = append(predecessors, fqreq)
		}
		successors[fqslice] = predecessors
		pending = append(pending, slice.Essential...)
	}

	// Sort them up.
	var order []SliceKey
	for _, names := range tarjanSort(successors) {
		if len(names) > 1 {
			return nil, fmt.Errorf("essential loop detected: %s", strings.Join(names, ", "))
		}
		name := names[0]
		dot := strings.IndexByte(name, '_')
		order = append(order, SliceKey{name[:dot], name[dot+1:]})
	}

	return order, nil
}

// fnameExp matches the slice definition file basename.
var fnameExp = regexp.MustCompile(`^([a-z0-9](?:-?[.a-z0-9+]){1,})\.yaml$`)

// snameExp matches only the slice name, without the leading package name.
var snameExp = regexp.MustCompile(`^([a-z](?:-?[a-z0-9]){2,})$`)

// knameExp matches the slice full name in pkg_slice format.
var knameExp = regexp.MustCompile(`^([a-z0-9](?:-?[.a-z0-9+]){1,})_([a-z](?:-?[a-z0-9]){2,})$`)

func ParseSliceKey(sliceKey string) (SliceKey, error) {
	match := knameExp.FindStringSubmatch(sliceKey)
	if match == nil {
		return SliceKey{}, fmt.Errorf("invalid slice reference: %q", sliceKey)
	}
	return SliceKey{match[1], match[2]}, nil
}

func readRelease(baseDir string) (*Release, error) {
	baseDir = filepath.Clean(baseDir)
	filePath := filepath.Join(baseDir, "chisel.yaml")
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("cannot read release definition: %s", err)
	}
	release, err := parseRelease(baseDir, filePath, data)
	if err != nil {
		return nil, err
	}
	err = readSlices(release, baseDir, filepath.Join(baseDir, "slices"))
	if err != nil {
		return nil, err
	}
	return release, err
}

func readSlices(release *Release, baseDir, dirName string) error {
	entries, err := os.ReadDir(dirName)
	if err != nil {
		return fmt.Errorf("cannot read %s%c directory", stripBase(baseDir, dirName), filepath.Separator)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			err := readSlices(release, baseDir, filepath.Join(dirName, entry.Name()))
			if err != nil {
				return err
			}
			continue
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		match := fnameExp.FindStringSubmatch(entry.Name())
		if match == nil {
			return fmt.Errorf("invalid slice definition filename: %q", entry.Name())
		}

		pkgName := match[1]
		pkgPath := filepath.Join(dirName, entry.Name())
		if pkg, ok := release.Packages[pkgName]; ok {
			return fmt.Errorf("package %q slices defined more than once: %s and %s\")", pkgName, pkg.Path, pkgPath)
		}
		data, err := os.ReadFile(pkgPath)
		if err != nil {
			// Errors from package os generally include the path.
			return fmt.Errorf("cannot read slice definition file: %v", err)
		}

		pkg, err := parsePackage(baseDir, pkgName, stripBase(baseDir, pkgPath), data)
		if err != nil {
			return err
		}

		release.Packages[pkg.Name] = pkg
	}
	return nil
}

func stripBase(baseDir, path string) string {
	// Paths must be clean for this to work correctly.
	return strings.TrimPrefix(path, baseDir+string(filepath.Separator))
}

func Select(release *Release, slices []SliceKey) (*Selection, error) {
	logf("Selecting slices...")

	selection := &Selection{
		Release: release,
	}

	sorted, err := order(release.Packages, slices)
	if err != nil {
		return nil, err
	}
	selection.Slices = make([]*Slice, len(sorted))
	for i, key := range sorted {
		selection.Slices[i] = release.Packages[key.Package].Slices[key.Slice]
	}

	for _, new := range selection.Slices {
		for newPath, newInfo := range new.Contents {
			switch newInfo.Generate {
			case GenerateNone, GenerateManifest:
			default:
				return nil, fmt.Errorf("slice %s has invalid 'generate' for path %s: %q",
					new, newPath, newInfo.Generate)
			}
		}
	}

	return selection, nil
}

type conflictGraph struct {
	path    string
	release *Release
	// The initial node (pkg) of the chain. If the graph is not linear, then
	// head may be any node.
	head    string
	visited map[string]*Slice
}

// isLinear returns true if the 'prefer' relations form a linear graph.
func (g *conflictGraph) isLinear() bool {
	return g.visited[g.head].Contents[g.path].Prefer != ""
}

// hasNoPrefers returns true if there are at least two nodes in the graph and
// none of them have specified 'prefer' on the paths.
func (g *conflictGraph) hasNoPrefers() bool {
	return len(g.visited) > 1 && !g.isLinear()
}

// Returns the next node (pkg) in the 'prefer' chain.
func (g *conflictGraph) next(pkg string) string {
	return g.visited[pkg].Contents[g.path].Prefer
}

// findSlice returns a package slice which defines the path.
func (g *conflictGraph) findSlice(pkg string) (*Slice, error) {
	for _, s := range g.release.Packages[pkg].Slices {
		if _, ok := s.Contents[g.path]; ok {
			return s, nil
		}
	}
	return nil, fmt.Errorf("package %s does not have path %s", pkg, g.path)
}

// walk iterates over the 'prefer' relationships for the path, starting from
// src. It returns an error if it does not find a linear relationship between
// src and dest.
func (g *conflictGraph) walk(src, dest string) error {
	tempVisited := make(map[string]*Slice)
	var prev string
	cur := src
	for {
		if _, ok := tempVisited[cur]; ok {
			// Found a cycle.
			var cycle []string
			for u := cur; ; u = tempVisited[u].Contents[g.path].Prefer {
				if len(cycle) > 0 && u == cycle[0] {
					break
				}
				cycle = append(cycle, u)
			}
			idx := slices.Index(cycle, slices.Min(cycle))
			cycle = append(cycle[idx:], cycle[:idx]...)
			return fmt.Errorf("slice %s path %s has a 'prefer' cycle: %s",
				tempVisited[cycle[0]], g.path, strings.Join(cycle, ", "))
		}
		if _, ok := g.visited[cur]; ok {
			// This has been previously visited, thus this chain stops here.
			if cur != dest {
				// The chain should have stopped at dest, but since it does not,
				// it means that it is not a linear 'prefer' graph, rather a "Y"
				// shaped one.
				a, b := tempVisited[src], g.visited[dest]
				if a.String() > b.String() {
					a, b = b, a
				}
				return fmt.Errorf("slices %s and %s have a non-linear 'prefer' relationship for path %s",
					a, b, g.path)
			}
			break
		}
		s, err := g.findSlice(cur)
		if err != nil {
			if prev != "" {
				prevSlice := tempVisited[prev]
				return fmt.Errorf("slice %s path %s prefers %q: %w",
					prevSlice, g.path, cur, err)
			}
			return err
		}
		tempVisited[cur] = s

		next := s.Contents[g.path].Prefer
		if next == "" {
			// The tail of the chain is found. This chain stops here.
			if dest != "" {
				// A tail has been found whereas this chain should have stopped
				// at package dest. Thus the 'prefer' graph must have more than
				// one components i.e. disconnected.
				a, b := tempVisited[src], g.visited[dest]
				if a.String() > b.String() {
					a, b = b, a
				}
				return fmt.Errorf("slices %s and %s have no 'prefer' relationship for path %s",
					a, b, g.path)
			}
			break
		}
		if _, ok := g.release.Packages[next]; !ok {
			return fmt.Errorf("slice %s has invalid 'prefer' for path %s: %q", s, g.path, next)
		}
		prev = cur
		cur = next
	}

	// A chain is found. Update the head of the chain and mark the nodes.
	g.head = src
	for p, s := range tempVisited {
		g.visited[p] = s
	}
	return nil
}

// PackageProvidesPath returns true if pkg should be the one to provide path
// among all other selected packages.
// pkg provides the path if there are no conflicts regarding the path or if
// there is, pkg is the most _preferred_ package for this path in the selection.
func (s *Selection) PackageProvidesPath(pkg, path string) bool {
	ranks, ok := s.Release.ConflictRanks[path]
	if !ok {
		return true
	}
	pkgRank, ok := ranks[pkg]
	if !ok {
		return false
	}
	for _, slice := range s.Slices {
		if rank, ok := ranks[slice.Package]; ok {
			if rank > pkgRank {
				return false
			}
		}
	}
	return true
}
