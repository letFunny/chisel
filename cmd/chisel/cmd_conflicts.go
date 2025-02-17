package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jessevdk/go-flags"

	"github.com/canonical/chisel/internal/setup"
	"github.com/canonical/chisel/internal/strdist"
)

var shortConflictHelp = "Count number of conlicting implicit parent dirs"
var longConflictHelp = `
Count number of conlicting implicit parent dirs
`

var conflictDescs = map[string]string{
	"release": "Chisel release name or directory (e.g. ubuntu-22.04)",
	"details": "Show conflict details",
}

type cmdConflict struct {
	Release string `long:"release" value-name:"<dir>"`
	Details bool   `long:"details"`
}

func init() {
	addCommand(
		"conflicts", shortConflictHelp, longConflictHelp,
		func() flags.Commander { return &cmdConflict{} },
		conflictDescs, nil,
	)
}

func (cmd *cmdConflict) Execute(args []string) error {
	if len(args) > 0 {
		return ErrExtraArgs
	}

	release, err := obtainRelease(cmd.Release)
	if err != nil {
		return err
	}

	conflicts, err := getConflicts(release)
	if err != nil {
		return err
	}

	w := tabWriter()
	defer w.Flush()
	fmt.Fprintf(Stdout, "Total conflicts: %d\n", len(conflicts))
	if cmd.Details {
		keys := make([]string, 0, len(conflicts))
		for p := range conflicts {
			keys = append(keys, p)
		}
		sort.Strings(keys)
		for _, p := range keys {
			c := conflicts[p]
			fmt.Fprintf(w, "%s\t%s\t%s\n", p, c.path, c.reason)
		}
	}
	return nil
}

type conflictInfo struct {
	path   string
	reason string
}

// Get all conflicts.
//
// Assumptions:
//   - Same paths across slices/packages are considered the same path.
//   - Paths conflict if they share at least one ancestor.
func getConflicts(r *setup.Release) (map[string]*conflictInfo, error) {
	if r == nil {
		return nil, nil
	}

	var paths []string
	for _, pkg := range r.Packages {
		for _, slice := range pkg.Slices {
			for p := range slice.Contents {
				paths = append(paths, p)
			}
		}
	}

	c := make(map[string]*conflictInfo)
	for i, p := range paths {
		for _, q := range paths[:i] {
			prefix := hasConflict(p, q)
			if prefix != "" {
				c[p] = &conflictInfo{
					path:   q,
					reason: prefix,
				}
				c[q] = &conflictInfo{
					path:   p,
					reason: prefix,
				}
			}
		}
	}
	return c, nil
}

// Returns the conflicting prefix.
func hasConflict(p, q string) string {
	ps := strings.Split(p, "/")[1:]
	qs := strings.Split(q, "/")[1:]

	if len(ps) == 0 || len(qs) == 0 {
		return ""
	}

	if len(ps) == 1 || len(qs) == 1 {
		var wild bool
		a := ps[0]
		b := qs[0]
		if i := strings.Index(a, "**"); i != -1 {
			a = a[:i] + "**"
			wild = true
		}
		if i := strings.Index(b, "**"); i != -1 {
			b = b[:i] + "**"
			wild = true
		}
		if !wild {
			// One of them must have **.
			return ""
		}
		if a == b || strdist.GlobPath(a, b) {
			return "/" + a
		}
		return ""
	}

	ps = ps[:len(ps)-1]
	qs = qs[:len(qs)-1]

	if ps[0] == qs[0] {
		// First directory matches.
		return "/" + ps[0]
	}

	if strdist.GlobPath(ps[0], qs[0]) {
		return "/" + ps[0]
	}

	return ""
}
