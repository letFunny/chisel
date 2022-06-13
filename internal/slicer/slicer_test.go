package slicer_test

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"

	. "gopkg.in/check.v1"

	"github.com/canonical/chisel/internal/archive"
	"github.com/canonical/chisel/internal/setup"
	"github.com/canonical/chisel/internal/slicer"
	"github.com/canonical/chisel/internal/testutil"
)

type slicerTest struct {
	summary string
	release map[string]string
	slices  []setup.SliceKey
	result  map[string]string
	error   string
}

var slicerTests = []slicerTest{{
	summary: "Basic slicing",
	slices:  []setup.SliceKey{{"base-files", "myslice"}},
	release: map[string]string{
		"slices/mydir/base-files.yaml": `
			package: base-files
			slices:
				myslice:
					contents:
						/usr/bin/hello:
						/usr/bin/hallo: {copy: /usr/bin/hello}
						/bin/hallo:     {symlink: ../usr/bin/hallo}
						/etc/passwd:    {text: data1}
						/etc/dir/sub/:  {make: true, mode: 01775}
		`,
	},
	result: map[string]string{
		"/usr/":          "dir 0755",
		"/usr/bin/":      "dir 0755",
		"/usr/bin/hello": "file 0775 eaf29575",
		"/usr/bin/hallo": "file 0775 eaf29575",
		"/bin/":          "dir 0755",
		"/bin/hallo":     "symlink ../usr/bin/hallo",
		"/etc/":          "dir 0755",
		"/etc/dir/":      "dir 0755",
		"/etc/dir/sub/":  "dir 01775",
		"/etc/passwd":    "file 0644 5b41362b",
	},
}, {
	summary: "Glob extraction",
	slices:  []setup.SliceKey{{"base-files", "myslice"}},
	release: map[string]string{
		"slices/mydir/base-files.yaml": `
			package: base-files
			slices:
				myslice:
					contents:
						/**/he*o:
		`,
	},
	result: map[string]string{
		"/usr/":          "dir 0755",
		"/usr/bin/":      "dir 0755",
		"/usr/bin/hello": "file 0775 eaf29575",
	},
}, {
	summary: "Create new file under extracted directory",
	slices:  []setup.SliceKey{{"base-files", "myslice"}},
	release: map[string]string{
		"slices/mydir/base-files.yaml": `
			package: base-files
			slices:
				myslice:
					contents:
						# Note the missing /tmp/ here.
						/tmp/new: {text: data1}
		`,
	},
	result: map[string]string{
		"/tmp/":    "dir 01775", // This is the magic.
		"/tmp/new": "file 0644 5b41362b",
	},
}, {
	summary: "Create new nested file under extracted directory",
	slices:  []setup.SliceKey{{"base-files", "myslice"}},
	release: map[string]string{
		"slices/mydir/base-files.yaml": `
			package: base-files
			slices:
				myslice:
					contents:
						# Note the missing /tmp/ here.
						/tmp/new/sub: {text: data1}
		`,
	},
	result: map[string]string{
		"/tmp/":        "dir 01775", // This is the magic.
		"/tmp/new/":    "dir 0755",
		"/tmp/new/sub": "file 0644 5b41362b",
	},
}, {
	summary: "Create new directory under extracted directory",
	slices:  []setup.SliceKey{{"base-files", "myslice"}},
	release: map[string]string{
		"slices/mydir/base-files.yaml": `
			package: base-files
			slices:
				myslice:
					contents:
						# Note the missing /tmp/ here.
						/tmp/new/: {make: true}
		`,
	},
	result: map[string]string{
		"/tmp/":     "dir 01775", // This is the magic.
		"/tmp/new/": "dir 0755",
	},
}, {
	summary: "Script: write a file",
	slices:  []setup.SliceKey{{"base-files", "myslice"}},
	release: map[string]string{
		"slices/mydir/base-files.yaml": `
			package: base-files
			slices:
				myslice:
					contents:
						/tmp/file1: {text: data1, mutable: true}
					mutate: |
						content.write("/tmp/file1", "data2")
		`,
	},
	result: map[string]string{
		"/tmp/":      "dir 01775",
		"/tmp/file1": "file 0644 d98cf53e",
	},
}, {
	summary: "Script: read a file",
	slices:  []setup.SliceKey{{"base-files", "myslice"}},
	release: map[string]string{
		"slices/mydir/base-files.yaml": `
			package: base-files
			slices:
				myslice:
					contents:
						/tmp/file1: {text: data1}
						/foo/file2: {text: data2, mutable: true}
					mutate: |
						data = content.read("/tmp/file1")
						content.write("/foo/file2", data)
		`,
	},
	result: map[string]string{
		"/tmp/":      "dir 01775",
		"/tmp/file1": "file 0644 5b41362b",
		"/foo/":      "dir 0755",
		"/foo/file2": "file 0644 5b41362b",
	},
}, {
	summary: "Script: use 'until' to remove file after mutate",
	slices:  []setup.SliceKey{{"base-files", "myslice"}},
	release: map[string]string{
		"slices/mydir/base-files.yaml": `
			package: base-files
			slices:
				myslice:
					contents:
						/tmp/file1: {text: data1, until: mutate}
						/foo/file2: {text: data2, mutable: true}
					mutate: |
						data = content.read("/tmp/file1")
						content.write("/foo/file2", data)
		`,
	},
	result: map[string]string{
		"/tmp/":      "dir 01775",
		"/foo/":      "dir 0755",
		"/foo/file2": "file 0644 5b41362b",
	},
}, {
	summary: "Script: cannot write non-mutable files",
	slices:  []setup.SliceKey{{"base-files", "myslice"}},
	release: map[string]string{
		"slices/mydir/base-files.yaml": `
			package: base-files
			slices:
				myslice:
					contents:
						/tmp/file1: {text: data1}
					mutate: |
						content.write("/tmp/file1", "data2")
		`,
	},
	error: `slice base-files_myslice: cannot write file not mutable: /tmp/file1`,
}, {
	summary: "Script: cannot read unlisted content",
	slices:  []setup.SliceKey{{"base-files", "myslice2"}},
	release: map[string]string{
		"slices/mydir/base-files.yaml": `
			package: base-files
			slices:
				myslice1:
					contents:
						/tmp/file1: {text: data1}
				myslice2:
					mutate: |
						content.read("/tmp/file1")
		`,
	},
	error: `slice base-files_myslice2: cannot read file not selected: /tmp/file1`,
}}

const defaultChiselYaml = `
	format: chisel-v1
	archives:
		ubuntu:
			version: 22.04
			components: [main, universe]
`

type testArchive struct {
	pkgs map[string][]byte
}

func (a *testArchive) Fetch(pkg string) (io.ReadCloser, error) {
	if data, ok := a.pkgs[pkg]; ok {
		return ioutil.NopCloser(bytes.NewBuffer(data)), nil
	}
	return nil, fmt.Errorf("attempted to open %q package", pkg)
}

func (a *testArchive) Exists(pkg string) bool {
	_, ok := a.pkgs[pkg]
	return ok
}

func (s *S) TestRun(c *C) {
	for _, test := range slicerTests {
		c.Logf("Summary: %s", test.summary)

		if _, ok := test.release["chisel.yaml"]; !ok {
			test.release["chisel.yaml"] = string(defaultChiselYaml)
		}

		releaseDir := c.MkDir()
		for path, data := range test.release {
			fpath := filepath.Join(releaseDir, path)
			err := os.MkdirAll(filepath.Dir(fpath), 0755)
			c.Assert(err, IsNil)
			err = ioutil.WriteFile(fpath, testutil.Reindent(data), 0644)
			c.Assert(err, IsNil)
		}

		release, err := setup.ReadRelease(releaseDir)
		c.Assert(err, IsNil)

		selection, err := setup.Select(release, test.slices)
		c.Assert(err, IsNil)

		archives := map[string]archive.Archive{
			"ubuntu": &testArchive{
				pkgs: map[string][]byte{
					"base-files": testutil.PackageData["base-files"],
				},
			},
		}

		targetDir := c.MkDir()
		options := slicer.RunOptions{
			Selection: selection,
			Archives:  archives,
			TargetDir: targetDir,
		}
		err = slicer.Run(&options)
		if test.error == "" {
			c.Assert(err, IsNil)
		} else {
			c.Assert(err, ErrorMatches, test.error)
			continue
		}

		c.Assert(testutil.TreeDump(targetDir), DeepEquals, test.result)
	}
}
