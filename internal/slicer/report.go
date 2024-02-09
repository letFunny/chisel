package slicer

import (
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/canonical/chisel/internal/fsutil"
	"github.com/canonical/chisel/internal/setup"
	"github.com/canonical/chisel/internal/strdist"
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
	// MarkedGlob holds the globs that will include their leaf fs entry in the
	// output of the report.
	MarkedGlob []string
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

func (r *Report) Add(slice *setup.Slice, info *fsutil.Info) error {
	relPath, err := filepath.Rel(r.Root, info.Path)
	if err != nil {
		return fmt.Errorf("internal error: cannot add path %q outside out root %q", info.Path, r.Root)
	}
	relPath = "/" + relPath

	// Check if the path is marked explicitly or if it matches a glob.
	if !r.Marked[relPath] {
		found := false
		for _, glob := range r.MarkedGlob {
			if strdist.GlobPath(glob, relPath) {
				found = true
				break
			}
		}
		if !found {
			return nil
		}
	}

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

// Mark marks the path as relevant when outputting the report.
func (r *Report) Mark(path string) {
	r.Marked[filepath.Clean(path)] = true
}

// MarkGlob marks the glob as relevant when outputting the report. Only the leaf
// fs entry of the glob will be considered.
func (r *Report) MarkGlob(path string) {
	r.MarkedGlob = append(r.MarkedGlob, path)
}
