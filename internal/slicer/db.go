package slicer

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/klauspost/compress/zstd"

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

// LocateManifestSlices finds the paths marked with "generate:manifest" and
// returns a map from said path to all the slices that declare it.
// TODO change visibility or move it to another package.
func LocateManifestSlices(slices []*setup.Slice) map[string][]*setup.Slice {
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

func ReadManifest(rootDir string, relPath string) ([]dbPath, error) {
	absPath := filepath.Join(rootDir, relPath)
	file, err := os.OpenFile(absPath, os.O_RDONLY, dbMode)
	if err != nil {
		return nil, err
	}
	r, err := zstd.NewReader(file)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	jsonwallDB, err := jsonwall.ReadDB(r)
	if err != nil {
		return nil, err
	}
	iter, err := jsonwallDB.Iterate(map[string]string{"kind": "path"})
	if err != nil {
		return nil, err
	}
	var paths []dbPath
	for iter.Next() {
		var path dbPath
		err := iter.Get(&path)
		if err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}
	return paths, nil
}

func TreeDumpManifest(entries []dbPath) map[string]string {
	result := make(map[string]string)
	for _, entry := range entries {
		var fsDump string
		switch {
		case strings.HasSuffix(entry.Path, "/"):
			fsDump = fmt.Sprintf("dir %s", entry.Mode)
		case entry.Link != "":
			fsDump = fmt.Sprintf("symlink %s", entry.Link)
		default: // Regular
			if entry.Size == 0 {
				fsDump = fmt.Sprintf("file %s empty", entry.Mode)
			} else if entry.FinalHash != "" {
				fsDump = fmt.Sprintf("file %s %s %s", entry.Mode, entry.Hash[:8], entry.FinalHash[:8])
			} else {
				fsDump = fmt.Sprintf("file %s %s", entry.Mode, entry.Hash[:8])
			}
		}

		// append {slice1, ..., sliceN} to the end of the entry dump.
		slicesStr := make([]string, 0, len(entry.Slices))
		for _, slice := range entry.Slices {
			slicesStr = append(slicesStr, slice)
		}
		sort.Strings(slicesStr)
		result[entry.Path] = fmt.Sprintf("%s {%s}", fsDump, strings.Join(slicesStr, ","))
	}
	return result
}

/* db.go */
