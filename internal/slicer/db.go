package slicer

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/canonical/chisel/internal/archive"
	"github.com/canonical/chisel/internal/jsonwall"
	"github.com/canonical/chisel/internal/setup"
)

func unixPerm(mode fs.FileMode) (perm uint32) {
	perm = uint32(mode.Perm())
	if mode&fs.ModeSticky != 0 {
		perm |= 01000
	}
	return perm
}

const dbMode fs.FileMode = 0644

type generateDBOptions struct {
	// Map of slices indexed by paths which contain an entry tagged "generate: manifest".
	ManifestSlices map[string][]*setup.Slice
	PackageInfo    []*archive.PackageInfo
	Slices         []*setup.Slice
	Report         *Report
}

// generateDB generates the Chisel manifest(s) at the specified paths. It
// returns the paths inside the rootfs where the manifest(s) are generated.
func generateDB(options *generateDBOptions) (*jsonwall.DBWriter, error) {
	dbw := jsonwall.NewDBWriter(&jsonwall.DBWriterOptions{
		Schema: dbSchema,
	})

	// Add packages to the db.
	for _, info := range options.PackageInfo {
		err := dbw.Add(&dbPackage{
			Kind:    "package",
			Name:    info.Name,
			Version: info.Version,
			Digest:  info.Hash,
			Arch:    info.Arch,
		})
		if err != nil {
			return nil, err
		}
	}
	// Add slices to the db.
	for _, s := range options.Slices {
		err := dbw.Add(&dbSlice{
			Kind: "slice",
			Name: s.String(),
		})
		if err != nil {
			return nil, err
		}
	}
	// Add paths and contents to the db.
	for _, entry := range options.Report.Entries {
		sliceNames := []string{}
		for s := range entry.Slices {
			err := dbw.Add(&dbContent{
				Kind:  "content",
				Slice: s.String(),
				Path:  entry.Path,
			})
			if err != nil {
				return nil, err
			}
			sliceNames = append(sliceNames, s.String())
		}
		sort.Strings(sliceNames)
		err := dbw.Add(&dbPath{
			Kind:      "path",
			Path:      entry.Path,
			Mode:      fmt.Sprintf("0%o", unixPerm(entry.Mode)),
			Slices:    sliceNames,
			Hash:      entry.Hash,
			FinalHash: entry.FinalHash,
			Size:      uint64(entry.Size),
			Link:      entry.Link,
		})
		if err != nil {
			return nil, err
		}
	}
	// Add the manifest path and content entries to the db.
	for path, slices := range options.ManifestSlices {
		fPath := getManifestPath(path)
		sliceNames := []string{}
		for _, s := range slices {
			err := dbw.Add(&dbContent{
				Kind:  "content",
				Slice: s.String(),
				Path:  fPath,
			})
			if err != nil {
				return nil, err
			}
			sliceNames = append(sliceNames, s.String())
		}
		sort.Strings(sliceNames)
		err := dbw.Add(&dbPath{
			Kind:   "path",
			Path:   fPath,
			Mode:   fmt.Sprintf("0%o", unixPerm(dbMode)),
			Slices: sliceNames,
		})
		if err != nil {
			return nil, err
		}
	}

	return dbw, nil
}

/* db.go */

const dbFile = "chisel.db"
const dbSchema = "1.0"

type dbPackage struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Digest  string `json:"sha256"`
	Arch    string `json:"arch"`
}

type dbSlice struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

type dbPath struct {
	Kind      string   `json:"kind"`
	Path      string   `json:"path"`
	Mode      string   `json:"mode"`
	Slices    []string `json:"slices"`
	Hash      string   `json:"sha256,omitempty"`
	FinalHash string   `json:"final_sha256,omitempty"`
	Size      uint64   `json:"size,omitempty"`
	Link      string   `json:"link,omitempty"`
}

type dbContent struct {
	Kind  string `json:"kind"`
	Slice string `json:"slice"`
	Path  string `json:"path"`
}

// getManifestPath parses the "generate" glob path to get the regular path to its
// directory.
// TODO combine with isManifestPath or whatever it was called + bool flag.
func getManifestPath(generatePath string) string {
	dir := filepath.Clean(strings.TrimSuffix(generatePath, "**"))
	return filepath.Join(dir, dbFile)
}

// locateManifestSlices finds the paths marked with "generate:manifest" and
// returns a map from said path to all the slices that declare it.
func locateManifestSlices(slices []*setup.Slice) map[string][]*setup.Slice {
	manifestSlices := make(map[string][]*setup.Slice)
	for _, s := range slices {
		for path, info := range s.Contents {
			if info.Generate == setup.GenerateManifest {
				if manifestSlices[path] == nil {
					manifestSlices[path] = []*setup.Slice{}
				}
				manifestSlices[path] = append(manifestSlices[path], s)
			}
		}
	}
	return manifestSlices
}

/* db.go */
