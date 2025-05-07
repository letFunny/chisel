package main

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"slices"

	"github.com/jessevdk/go-flags"

	"github.com/canonical/chisel/internal/archive"
	"github.com/canonical/chisel/internal/cache"
	"github.com/canonical/chisel/internal/deb"
)

type cmdDebugCohesion struct {
	Release string `long:"release" value-name:"<branch|dir>"`
	Arch    string `long:"arch" value-name:"<arch>"`
}

func (cmd *cmdDebugCohesion) Execute(args []string) error {
	release, err := obtainRelease(cmd.Release)
	if err != nil {
		return err
	}

	archives := make(map[string]archive.Archive)
	for archiveName, archiveInfo := range release.Archives {
		openArchive, err := archive.Open(&archive.Options{
			Label:      archiveName,
			Version:    archiveInfo.Version,
			Arch:       cmd.Arch,
			Suites:     archiveInfo.Suites,
			Components: archiveInfo.Components,
			Pro:        archiveInfo.Pro,
			CacheDir:   cache.DefaultDir("chisel"),
			PubKeys:    archiveInfo.PubKeys,
		})
		if err != nil {
			if err == archive.ErrCredentialsNotFound {
				fmt.Fprintf(os.Stderr, "Archive %q ignored: credentials not found\n", archiveName)
				continue
			}
			return err
		}
		archives[archiveName] = openArchive
	}

	type ownership struct {
		mode int64
		link string
		pkgs []string
	}
	directories := map[string][]ownership{}
	for archiveName, archive := range archives {
		fmt.Fprintf(os.Stderr, "archive %s\n", archiveName)
		for pkgName, _ := range release.Packages {
			fmt.Fprintf(os.Stderr, "processing %s\n", pkgName)
			if !archive.Exists(pkgName) {
				continue
			}
			pkgReader, _, err := archive.Fetch(pkgName)
			if err != nil {
				return err
			}
			dataReader, err := deb.DataReader(pkgReader)
			if err != nil {
				return err
			}
			tarReader := tar.NewReader(dataReader)
			for {
				tarHeader, err := tarReader.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					return err
				}

				path, ok := sanitizeTarPath(tarHeader.Name)
				if !ok {
					continue
				}
				isDir := path[len(path)-1] == '/'
				if !isDir && tarHeader.Linkname == "" {
					// TODO false positives with symlinks that do not point to dirs.
					continue
				}
				if isDir {
					// Remove trailing '/' to make paths uniform. While directories
					// always end in '/', symlinks don't.
					path = path[:len(path)-1]
				}

				data := directories[path]
				found := false
				// We look for a previous package that has the same entry in
				// terms of mode, link, etc. If there is none we record this
				// package as owning the path.
				for i, o := range data {
					if tarHeader.Linkname != "" {
						if tarHeader.Linkname == o.link {
							o.pkgs = append(o.pkgs, pkgName)
							data[i] = o
							found = true
							break
						}
					} else {
						if tarHeader.Mode == o.mode {
							o.pkgs = append(o.pkgs, pkgName)
							data[i] = o
							found = true
							break
						}
					}
				}
				if !found {
					data = append(data, ownership{
						mode: tarHeader.Mode,
						link: tarHeader.Linkname,
						pkgs: []string{pkgName},
					})
					directories[path] = data
				}
			}
		}
	}

	var orderedDirs []string
	for dir, data := range directories {
		if len(data) == 1 {
			continue
		}
		orderedDirs = append(orderedDirs, dir)
	}
	slices.Sort(orderedDirs)
	for _, dir := range orderedDirs {
		fmt.Printf("%s:\n", dir)
		data := directories[dir]
		for _, o := range data {
			var pkgsStr string
			if len(o.pkgs) <= 3 {
				pkgsStr = fmt.Sprintf("%s", o.pkgs)
			} else {
				pkgsStr = fmt.Sprintf("[%s,%s,%s,...(and %d more)]", o.pkgs[0], o.pkgs[1], o.pkgs[2], len(o.pkgs)-3)
			}
			fmt.Printf("\t{mode: 0%o, link: %q, pkgs: %s}\n", o.mode, o.link, pkgsStr)
		}
	}

	return nil
}

// sanitizeTarPath removes the leading "./" from the source path in the tarball,
// and verifies that the path is not empty.
func sanitizeTarPath(path string) (string, bool) {
	if len(path) < 3 || path[0] != '.' || path[1] != '/' {
		return "", false
	}
	return path[1:], true
}

func init() {
	// TODO: this should be debug command with no help and not shown by default.
	addCommand("check-cohesion", shortCutHelp, longCutHelp, func() flags.Commander { return &cmdDebugCohesion{} }, cutDescs, nil)
}
