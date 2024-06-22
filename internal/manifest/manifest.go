package manifest

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"

	"github.com/canonical/chisel/internal/jsonwall"
	"github.com/canonical/chisel/internal/setup"
)

const Filename = "manifest.wall"
const Schema = "1.0"
const Mode fs.FileMode = 0644

type Package struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Digest  string `json:"sha256"`
	Arch    string `json:"arch"`
}

type Slice struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

type Path struct {
	Kind      string   `json:"kind"`
	Path      string   `json:"path"`
	Mode      string   `json:"mode"`
	Slices    []string `json:"slices"`
	Hash      string   `json:"sha256,omitempty"`
	FinalHash string   `json:"final_sha256,omitempty"`
	Size      uint64   `json:"size,omitempty"`
	Link      string   `json:"link,omitempty"`
}

type Content struct {
	Kind  string `json:"kind"`
	Slice string `json:"slice"`
	Path  string `json:"path"`
}

// GetManifestPath parses the "generate" glob path to get the regular path to its
// directory.
// TODO combine with isManifestPath or whatever it was called + bool flag.
func GetManifestPath(generatePath string) string {
	dir := filepath.Clean(strings.TrimSuffix(generatePath, "**"))
	return filepath.Join(dir, Filename)
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

type Manifest struct {
	Paths    []Path
	Contents []Content
	Packages []Package
	Slices   []Slice
}

// TODO a function to validate?

func ReadManifest(rootDir string, relPath string) (*Manifest, error) {
	absPath := filepath.Join(rootDir, relPath)
	file, err := os.OpenFile(absPath, os.O_RDONLY, Mode)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	r, err := zstd.NewReader(file)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	jsonwallDB, err := jsonwall.ReadDB(r)
	if err != nil {
		return nil, err
	}
	var manifest Manifest
	iter, err := jsonwallDB.Iterate(map[string]string{"kind": "path"})
	if err != nil {
		return nil, err
	}
	for iter.Next() {
		var path Path
		err := iter.Get(&path)
		if err != nil {
			return nil, err
		}
		manifest.Paths = append(manifest.Paths, path)
	}
	iter, err = jsonwallDB.Iterate(map[string]string{"kind": "content"})
	if err != nil {
		return nil, err
	}
	for iter.Next() {
		var content Content
		err := iter.Get(&content)
		if err != nil {
			return nil, err
		}
		manifest.Contents = append(manifest.Contents, content)
	}
	iter, err = jsonwallDB.Iterate(map[string]string{"kind": "package"})
	if err != nil {
		return nil, err
	}
	for iter.Next() {
		var pkg Package
		err := iter.Get(&pkg)
		if err != nil {
			return nil, err
		}
		manifest.Packages = append(manifest.Packages, pkg)
	}
	iter, err = jsonwallDB.Iterate(map[string]string{"kind": "slice"})
	if err != nil {
		return nil, err
	}
	for iter.Next() {
		var slice Slice
		err := iter.Get(&slice)
		if err != nil {
			return nil, err
		}
		manifest.Slices = append(manifest.Slices, slice)
	}
	return &manifest, nil
}
