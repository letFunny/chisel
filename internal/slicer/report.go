package slicer

import (
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/canonical/chisel/internal/fsutil"
	"github.com/canonical/chisel/internal/setup"
)

type ReportEntry struct {
	Path   string
	Mode   fs.FileMode
	Hash   string
	Size   int
	Slices map[*setup.Slice]bool
	Link   string
}

// Report holds the information about files and directories created when slicing
// packages.
type Report struct {
	// Root is the filesystem path where the all reported content is based.
	Root string
	// Entries holds all reported content, indexed by their path.
	Entries map[string]ReportEntry
	// Marked holds all the paths that are included in the output of the report.
	Marked map[string]bool
}

// NewReport returns an empty report for content that will be based at the
// provided root path.
func NewReport(root string) *Report {
	return &Report{
		Root:    root,
		Entries: make(map[string]ReportEntry),
		Marked:  make(map[string]bool),
	}
}

// Add expects an absolute path in info for a file/directory inside the report
// root.
func (r *Report) Add(slice *setup.Slice, info *fsutil.Info) error {
	if len(info.Path) < len(r.Root) || info.Path[:len(r.Root)] != r.Root {
		return fmt.Errorf("internal error: cannot add path %q outside out root %q", info.Path, r.Root)
	}
	relPath := filepath.Clean("/" + info.Path[len(r.Root):])

	if entry, ok := r.Entries[relPath]; ok {
		if info.Mode != entry.Mode || info.Link != entry.Link ||
			info.Size != entry.Size || info.Hash != entry.Hash {
			return fmt.Errorf("internal error: cannot add conflicting data for path %q", relPath)
		}
		entry.Slices[slice] = true
		r.Entries[relPath] = entry
	} else {
		r.Entries[relPath] = ReportEntry{
			Path:   relPath,
			Mode:   info.Mode,
			Hash:   info.Hash,
			Size:   info.Size,
			Slices: map[*setup.Slice]bool{slice: true},
			Link:   info.Link,
		}
	}
	return nil

}

// Collect returns only the relevant report entries.
// See [Report.Mark].
func (r *Report) Collect() map[string]ReportEntry {
	res := make(map[string]ReportEntry)
	for _, entry := range r.Entries {
		// Check if the path is marked explicitly.
		if r.Marked[entry.Path] {
			res[entry.Path] = entry
		}
	}
	return res
}

// Mark marks the path as relevant when outputting the report.
func (r *Report) Mark(path string) {
	r.Marked[filepath.Clean(path)] = true
}
