package main

import (
	"archive/tar"
	"fmt"
	"io"
	"os"

	"github.com/jessevdk/go-flags"
	"gopkg.in/yaml.v3"

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
		Mode yamlMode `yaml:"mode"`
		Link string   `yaml:"link"`
		// Pkgs is a correspondence from archive name to package names.
		Pkgs map[string][]string `yaml:"packages"`
	}

	directories := map[string][]ownership{}
	for archiveName, archive := range archives {
		logf("Processing archive %s", archiveName)
		for pkgName, _ := range release.Packages {
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
						if tarHeader.Linkname == o.Link {
							o.Pkgs[archiveName] = append(o.Pkgs[archiveName], pkgName)
							data[i] = o
							found = true
							break
						}
					} else {
						if tarHeader.Mode == int64(o.Mode) {
							o.Pkgs[archiveName] = append(o.Pkgs[archiveName], pkgName)
							data[i] = o
							found = true
							break
						}
					}
				}
				if !found {
					data = append(data, ownership{
						Mode: yamlMode(tarHeader.Mode),
						Link: tarHeader.Linkname,
						Pkgs: map[string][]string{archiveName: []string{pkgName}},
					})
					directories[path] = data
				}
			}
		}
	}

	problematic := map[string][]ownership{}
	for dir, o := range directories {
		if len(o) > 1 {
			problematic[dir] = o
		}
	}
	yb, err := yaml.Marshal(problematic)
	if err != nil {
		return nil
	}
	fmt.Fprintf(Stdout, "%s", string(yb))

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

type yamlMode int64

func (ym yamlMode) MarshalYAML() (interface{}, error) {
	// Workaround for marshalling integers in octal format.
	// Ref: https://github.com/go-yaml/yaml/issues/420.
	node := &yaml.Node{}
	err := node.Encode(uint(ym))
	if err != nil {
		return nil, err
	}
	node.Value = fmt.Sprintf("0%o", ym)
	return node, nil
}

var _ yaml.Marshaler = yamlMode(0)

func init() {
	// TODO: this should be debug command with no help and not shown by default.
	addCommand("check-cohesion", shortCutHelp, longCutHelp, func() flags.Commander { return &cmdDebugCohesion{} }, cutDescs, nil)
}
