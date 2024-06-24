package manifest

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

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

// GetManifestPath parses the "generate" glob path and returns the path to
// the manifest within that directory.
// TODO no me gusta esta función.
func GetManifestPath(generatePath string) (string, error) {
	dir, err := setup.GetGeneratePath(generatePath)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, Filename), nil
}

// LocateManifestSlices finds the paths marked with "generate:manifest" and
// returns a map from path to all the slices that declare it.
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

	manifest := &Manifest{}
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
	err = Validate(manifest)
	if err != nil {
		return nil, err
	}
	return manifest, nil
}

func Validate(manifest *Manifest) (err error) {
	defer func() {
		err = fmt.Errorf("invalid manifest: %s", err)
	}()

	pkgExist := map[string]bool{}
	for _, pkg := range manifest.Packages {
		if pkg.Kind != "package" {
			return fmt.Errorf("")
		}
		pkgExist[pkg.Name] = true
	}
	sliceExist := map[string]bool{}
	for _, slice := range manifest.Slices {
		if slice.Kind != "slice" {
			return fmt.Errorf("")
		}
		sliceExist[slice.Name] = true
	}
	pathToSlices := map[string][]string{}
	for _, content := range manifest.Contents {
		if content.Kind != "content" {
			return fmt.Errorf("")
		}
		if _, ok := sliceExist[content.Slice]; !ok {
			return fmt.Errorf("TODO")
		}
		pathToSlices[content.Path] = append(pathToSlices[content.Path], content.Slice)
	}
	for _, path := range manifest.Paths {
		if path.Kind != "path" {
			return fmt.Errorf("")
		}
		if pathSlices, ok := pathToSlices[path.Path]; !ok {
			return fmt.Errorf("TODO")
		} else if !slices.Equal(pathSlices, path.Slices) {
			return fmt.Errorf("TODO")
		}
	}
	return nil
}
