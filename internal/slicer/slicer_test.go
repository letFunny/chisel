package slicer_test

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	. "gopkg.in/check.v1"

	"github.com/canonical/chisel/internal/archive"
	"github.com/canonical/chisel/internal/setup"
	"github.com/canonical/chisel/internal/slicer"
	"github.com/canonical/chisel/internal/testutil"
)

var (
	testKey = testutil.PGPKeys["key1"]
)

type slicerTest struct {
	summary      string
	arch         string
	release      map[string]string
	slices       []setup.SliceKey
	hackopt      func(c *C, opts *slicer.RunOptions)
	fsResult     map[string]string
	reportResult map[string]string
	error        string
}

var packageEntries = map[string][]testutil.TarEntry{
	"copyright-symlink-libssl3": {
		{Header: tar.Header{Name: "./"}},
		{Header: tar.Header{Name: "./usr/"}},
		{Header: tar.Header{Name: "./usr/lib/"}},
		{Header: tar.Header{Name: "./usr/lib/x86_64-linux-gnu/"}},
		{Header: tar.Header{Name: "./usr/lib/x86_64-linux-gnu/libssl.so.3", Mode: 00755}},
		{Header: tar.Header{Name: "./usr/share/"}},
		{Header: tar.Header{Name: "./usr/share/doc/"}},
		{Header: tar.Header{Name: "./usr/share/doc/copyright-symlink-libssl3/"}},
		{Header: tar.Header{Name: "./usr/share/doc/copyright-symlink-libssl3/copyright"}},
	},
	"copyright-symlink-openssl": {
		{Header: tar.Header{Name: "./"}},
		{Header: tar.Header{Name: "./etc/"}},
		{Header: tar.Header{Name: "./etc/ssl/"}},
		{Header: tar.Header{Name: "./etc/ssl/openssl.cnf"}},
		{Header: tar.Header{Name: "./usr/"}},
		{Header: tar.Header{Name: "./usr/bin/"}},
		{Header: tar.Header{Name: "./usr/bin/openssl", Mode: 00755}},
		{Header: tar.Header{Name: "./usr/share/"}},
		{Header: tar.Header{Name: "./usr/share/doc/"}},
		{Header: tar.Header{Name: "./usr/share/doc/copyright-symlink-openssl/"}},
		{Header: tar.Header{Name: "./usr/share/doc/copyright-symlink-openssl/copyright", Linkname: "../libssl3/copyright"}},
	},
}

// Hardcoded copyright files from test-package package that will be automatically
// injected into every slice.
var copyrightEntries = map[string]string{
	"/usr/":                                 "dir 0755",
	"/usr/share/":                           "dir 0755",
	"/usr/share/doc/":                       "dir 0755",
	"/usr/share/doc/test-package/":          "dir 0755",
	"/usr/share/doc/test-package/copyright": "file 0644 c2fca2aa",
}

var slicerTests = []slicerTest{{
	summary: "Basic slicing",
	slices:  []setup.SliceKey{{"test-package", "myslice"}},
	release: map[string]string{
		"slices/mydir/test-package.yaml": `
			package: test-package
			slices:
				myslice:
					contents:
						/dir/file:
						/dir/file_copy:  {copy: /dir/file}
						/other_dir/file: {symlink: ../dir/file}
						/dir/text_file:  {text: data1}
						/dir/foo/bar/:   {make: true, mode: 01777}
		`,
	},
	fsResult: map[string]string{
		"/dir/":           "dir 0755",
		"/dir/file":       "file 0644 cc55e2ec",
		"/dir/file_copy":  "file 0644 cc55e2ec",
		"/dir/foo/":       "dir 0755",
		"/dir/foo/bar/":   "dir 01777",
		"/dir/text_file":  "file 0644 5b41362b",
		"/other_dir/":     "dir 0755",
		"/other_dir/file": "symlink ../dir/file",
	},
	reportResult: map[string]string{
		"/dir/file":       "file 0644 cc55e2ec {test-package_myslice}",
		"/dir/file_copy":  "file 0644 cc55e2ec {test-package_myslice}",
		"/dir/foo/bar/":   "dir 01777 {test-package_myslice}",
		"/dir/text_file":  "file 0644 5b41362b {test-package_myslice}",
		"/other_dir/file": "symlink ../dir/file {test-package_myslice}",
	},
}, {
	summary: "Glob extraction",
	slices:  []setup.SliceKey{{"test-package", "myslice"}},
	release: map[string]string{
		"slices/mydir/test-package.yaml": `
			package: test-package
			slices:
				myslice:
					contents:
						/**/other_f*e:
		`,
	},
	fsResult: map[string]string{
		"/dir/":                  "dir 0755",
		"/dir/nested/":           "dir 0755",
		"/dir/nested/other_file": "file 0644 6b86b273",
		"/dir/other_file":        "file 0644 63d5dd49",
	},
	reportResult: map[string]string{
		"/dir/nested/other_file": "file 0644 6b86b273 {test-package_myslice}",
		"/dir/other_file":        "file 0644 63d5dd49 {test-package_myslice}",
	},
}, {
	summary: "More glob extraction",
	slices:  []setup.SliceKey{{"test-package", "myslice"}},
	release: map[string]string{
		"slices/mydir/test-package.yaml": `
			package: test-package
			slices:
				myslice:
					contents:
						/dir/nested**:
		`,
	},
	fsResult: map[string]string{
		"/dir/":                  "dir 0755",
		"/dir/nested/":           "dir 0755",
		"/dir/nested/file":       "file 0644 84237a05",
		"/dir/nested/other_file": "file 0644 6b86b273",
	},
	reportResult: map[string]string{
		"/dir/nested/":           "dir 0755 {test-package_myslice}",
		"/dir/nested/file":       "file 0644 84237a05 {test-package_myslice}",
		"/dir/nested/other_file": "file 0644 6b86b273 {test-package_myslice}",
	},
}, {
	summary: "Create new file under extracted directory and preserve parent directory permissions",
	slices:  []setup.SliceKey{{"test-package", "myslice"}},
	release: map[string]string{
		"slices/mydir/test-package.yaml": `
			package: test-package
			slices:
				myslice:
					contents:
						# Note the missing parent directories here.
						/parent/new: {text: data1}
		`,
	},
	fsResult: map[string]string{
		"/parent/":    "dir 01777",
		"/parent/new": "file 0644 5b41362b",
	},
	reportResult: map[string]string{
		"/parent/new": "file 0644 5b41362b {test-package_myslice}",
	},
}, {
	summary: "Create new nested file under extracted directory and preserve parent directory permissions",
	slices:  []setup.SliceKey{{"test-package", "myslice"}},
	release: map[string]string{
		"slices/mydir/test-package.yaml": `
			package: test-package
			slices:
				myslice:
					contents:
						# Note the missing parent directories here.
						/parent/permissions/new: {text: data1}
		`,
	},
	fsResult: map[string]string{
		"/parent/":                "dir 01777",
		"/parent/permissions/":    "dir 0764",
		"/parent/permissions/new": "file 0644 5b41362b",
	},
	reportResult: map[string]string{
		"/parent/permissions/new": "file 0644 5b41362b {test-package_myslice}",
	},
}, {
	summary: "Create new directory under extracted directory",
	slices:  []setup.SliceKey{{"test-package", "myslice"}},
	release: map[string]string{
		"slices/mydir/test-package.yaml": `
			package: test-package
			slices:
				myslice:
					contents:
						# Note the missing parent directories here.
						/parent/new/: {make: true}
		`,
	},
	fsResult: map[string]string{
		"/parent/":     "dir 01777",
		"/parent/new/": "dir 0755",
	},
	reportResult: map[string]string{
		"/parent/new/": "dir 0755 {test-package_myslice}",
	},
}, {
	summary: "Conditional architecture",
	arch:    "amd64",
	slices:  []setup.SliceKey{{"test-package", "myslice"}},
	release: map[string]string{
		"slices/mydir/test-package.yaml": `
			package: test-package
			slices:
				myslice:
					contents:
						/dir/text_file_1: {text: data1, arch: amd64}
						/dir/text_file_2: {text: data1, arch: i386}
						/dir/text_file_3: {text: data1, arch: [i386, amd64]}
						/dir/nested/copy_1: {copy: /dir/nested/file, arch: amd64}
						/dir/nested/copy_2: {copy: /dir/nested/file, arch: i386}
						/dir/nested/copy_3: {copy: /dir/nested/file, arch: [i386, amd64]}
		`,
	},
	fsResult: map[string]string{
		"/dir/":              "dir 0755",
		"/dir/text_file_1":   "file 0644 5b41362b",
		"/dir/text_file_3":   "file 0644 5b41362b",
		"/dir/nested/":       "dir 0755",
		"/dir/nested/copy_1": "file 0644 84237a05",
		"/dir/nested/copy_3": "file 0644 84237a05",
	},
	reportResult: map[string]string{
		"/dir/nested/copy_1": "file 0644 84237a05 {test-package_myslice}",
		"/dir/nested/copy_3": "file 0644 84237a05 {test-package_myslice}",
		"/dir/text_file_1":   "file 0644 5b41362b {test-package_myslice}",
		"/dir/text_file_3":   "file 0644 5b41362b {test-package_myslice}",
	},
}, {
	summary: "Script: write a file",
	slices:  []setup.SliceKey{{"test-package", "myslice"}},
	release: map[string]string{
		"slices/mydir/test-package.yaml": `
			package: test-package
			slices:
				myslice:
					contents:
						/dir/text_file: {text: data1, mutable: true}
					mutate: |
						content.write("/dir/text_file", "data2")
		`,
	},
	fsResult: map[string]string{
		"/dir/":          "dir 0755",
		"/dir/text_file": "file 0644 d98cf53e",
	},
	reportResult: map[string]string{
		"/dir/text_file": "file 0644 5b41362b {test-package_myslice}",
	},
}, {
	summary: "Script: read a file",
	slices:  []setup.SliceKey{{"test-package", "myslice"}},
	release: map[string]string{
		"slices/mydir/test-package.yaml": `
			package: test-package
			slices:
				myslice:
					contents:
						/dir/text_file_1: {text: data1}
						/foo/text_file_2: {text: data2, mutable: true}
					mutate: |
						data = content.read("/dir/text_file_1")
						content.write("/foo/text_file_2", data)
		`,
	},
	fsResult: map[string]string{
		"/dir/":            "dir 0755",
		"/dir/text_file_1": "file 0644 5b41362b",
		"/foo/":            "dir 0755",
		"/foo/text_file_2": "file 0644 5b41362b",
	},
	reportResult: map[string]string{
		"/dir/text_file_1": "file 0644 5b41362b {test-package_myslice}",
		"/foo/text_file_2": "file 0644 d98cf53e {test-package_myslice}",
	},
}, {
	summary: "Script: use 'until' to remove file after mutate",
	slices:  []setup.SliceKey{{"test-package", "myslice"}},
	release: map[string]string{
		"slices/mydir/test-package.yaml": `
			package: test-package
			slices:
				myslice:
					contents:
						/dir/text_file_1: {text: data1, until: mutate}
						/foo/text_file_2: {text: data2, mutable: true}
					mutate: |
						data = content.read("/dir/text_file_1")
						content.write("/foo/text_file_2", data)
		`,
	},
	fsResult: map[string]string{
		"/dir/":            "dir 0755",
		"/foo/":            "dir 0755",
		"/foo/text_file_2": "file 0644 5b41362b",
	},
	reportResult: map[string]string{
		// TODO this path needs to be removed from the report.
		"/dir/text_file_1": "file 0644 5b41362b {test-package_myslice}",
		"/foo/text_file_2": "file 0644 d98cf53e {test-package_myslice}",
	},
}, {
	summary: "Script: use 'until' to remove wildcard after mutate",
	slices:  []setup.SliceKey{{"test-package", "myslice"}},
	release: map[string]string{
		"slices/mydir/test-package.yaml": `
			package: test-package
			slices:
				myslice:
					contents:
						/dir/nested**:  {until: mutate}
						/other_dir/text_file: {until: mutate, text: data1}
		`,
	},
	fsResult: map[string]string{
		"/dir/":       "dir 0755",
		"/other_dir/": "dir 0755",
	},
	reportResult: map[string]string{
		// TODO These first three entries should be removed from the report.
		"/dir/nested/":           "dir 0755 {test-package_myslice}",
		"/dir/nested/file":       "file 0644 84237a05 {test-package_myslice}",
		"/dir/nested/other_file": "file 0644 6b86b273 {test-package_myslice}",

		"/other_dir/text_file": "file 0644 5b41362b {test-package_myslice}",
	},
}, {
	summary: "Script: 'until' does not remove non-empty directories",
	slices:  []setup.SliceKey{{"test-package", "myslice"}},
	release: map[string]string{
		"slices/mydir/test-package.yaml": `
			package: test-package
			slices:
				myslice:
					contents:
						/dir/nested/: {until: mutate}
						/dir/nested/file_copy: {copy: /dir/file}
		`,
	},
	fsResult: map[string]string{
		"/dir/":                 "dir 0755",
		"/dir/nested/":          "dir 0755",
		"/dir/nested/file_copy": "file 0644 cc55e2ec",
	},
	reportResult: map[string]string{
		"/dir/nested/":          "dir 0755 {test-package_myslice}",
		"/dir/nested/file_copy": "file 0644 cc55e2ec {test-package_myslice}",
	},
}, {
	summary: "Script: cannot write non-mutable files",
	slices:  []setup.SliceKey{{"test-package", "myslice"}},
	release: map[string]string{
		"slices/mydir/test-package.yaml": `
			package: test-package
			slices:
				myslice:
					contents:
						/dir/text_file: {text: data1}
					mutate: |
						content.write("/dir/text_file", "data2")
		`,
	},
	error: `slice test-package_myslice: cannot write file which is not mutable: /dir/text_file`,
}, {
	summary: "Script: cannot read unlisted content",
	slices:  []setup.SliceKey{{"test-package", "myslice2"}},
	release: map[string]string{
		"slices/mydir/test-package.yaml": `
			package: test-package
			slices:
				myslice1:
					contents:
						/dir/text_file: {text: data1}
				myslice2:
					mutate: |
						content.read("/dir/text_file")
		`,
	},
	error: `slice test-package_myslice2: cannot read file which is not selected: /dir/text_file`,
}, {
	summary: "Script: can read globbed content",
	slices:  []setup.SliceKey{{"test-package", "myslice1"}, {"test-package", "myslice2"}},
	release: map[string]string{
		"slices/mydir/test-package.yaml": `
			package: test-package
			slices:
				myslice1:
					contents:
						/dir/nested/fil*:
				myslice2:
					mutate: |
						content.read("/dir/nested/file")
		`,
	},
}, {
	summary: "Relative content root directory must not error",
	slices:  []setup.SliceKey{{"test-package", "myslice"}},
	release: map[string]string{
		"slices/mydir/test-package.yaml": `
			package: test-package
			slices:
				myslice:
					contents:
						/dir/text_file: {text: data1}
					mutate: |
						content.read("/dir/text_file")
		`,
	},
	hackopt: func(c *C, opts *slicer.RunOptions) {
		dir, err := os.Getwd()
		c.Assert(err, IsNil)
		opts.TargetDir, err = filepath.Rel(dir, opts.TargetDir)
		c.Assert(err, IsNil)
	},
}, {
	summary: "Can list parent directories of normal paths",
	slices:  []setup.SliceKey{{"test-package", "myslice"}},
	release: map[string]string{
		"slices/mydir/test-package.yaml": `
			package: test-package
			slices:
				myslice:
					contents:
						/a/b/c: {text: foo}
						/x/y/: {make: true}
					mutate: |
						content.list("/")
						content.list("/a")
						content.list("/a/b")
						content.list("/x")
						content.list("/x/y")
		`,
	},
}, {
	summary: "Cannot list unselected directory",
	slices:  []setup.SliceKey{{"test-package", "myslice"}},
	release: map[string]string{
		"slices/mydir/test-package.yaml": `
			package: test-package
			slices:
				myslice:
					contents:
						/a/b/c: {text: foo}
					mutate: |
						content.list("/a/d")
		`,
	},
	error: `slice test-package_myslice: cannot list directory which is not selected: /a/d/`,
}, {
	summary: "Cannot list file path as a directory",
	slices:  []setup.SliceKey{{"test-package", "myslice"}},
	release: map[string]string{
		"slices/mydir/test-package.yaml": `
			package: test-package
			slices:
				myslice:
					contents:
						/a/b/c: {text: foo}
					mutate: |
						content.list("/a/b/c")
		`,
	},
	error: `slice test-package_myslice: content is not a directory: /a/b/c`,
}, {
	summary: "Can list parent directories of globs",
	slices:  []setup.SliceKey{{"test-package", "myslice"}},
	release: map[string]string{
		"slices/mydir/test-package.yaml": `
			package: test-package
			slices:
				myslice:
					contents:
						/**/nested/f?le:
					mutate: |
						content.list("/dir/nested")
		`,
	},
}, {
	summary: "Cannot list directories not matched by glob",
	slices:  []setup.SliceKey{{"test-package", "myslice"}},
	release: map[string]string{
		"slices/mydir/test-package.yaml": `
			package: test-package
			slices:
				myslice:
					contents:
						/**/nested/f?le:
					mutate: |
						content.list("/other_dir")
		`,
	},
	error: `slice test-package_myslice: cannot list directory which is not selected: /other_dir/`,
}, {
	summary: "Duplicate copyright symlink is ignored",
	slices:  []setup.SliceKey{{"copyright-symlink-openssl", "bins"}},
	release: map[string]string{
		"slices/mydir/copyright-symlink-libssl3.yaml": `
			package: copyright-symlink-libssl3
			slices:
				libs:
					contents:
						/usr/lib/x86_64-linux-gnu/libssl.so.3:
		`,
		"slices/mydir/copyright-symlink-openssl.yaml": `
			package: copyright-symlink-openssl
			slices:
				bins:
					essential:
						- copyright-symlink-libssl3_libs
						- copyright-symlink-openssl_config
					contents:
						/usr/bin/openssl:
				config:
					contents:
						/etc/ssl/openssl.cnf:
		`,
	},
}, {
	summary: "Can list unclean directory paths",
	slices:  []setup.SliceKey{{"test-package", "myslice"}},
	release: map[string]string{
		"slices/mydir/test-package.yaml": `
			package: test-package
			slices:
				myslice:
					contents:
						/a/b/c: {text: foo}
						/x/y/: {make: true}
					mutate: |
						content.list("/////")
						content.list("/a/")
						content.list("/a/b/../b/")
						content.list("/x///")
						content.list("/x/./././y")
		`,
	},
}, {
	summary: "Cannot read directories",
	slices:  []setup.SliceKey{{"test-package", "myslice"}},
	release: map[string]string{
		"slices/mydir/test-package.yaml": `
			package: test-package
			slices:
				myslice:
					contents:
						/x/y/: {make: true}
					mutate: |
						content.read("/x/y")
		`,
	},
	error: `slice test-package_myslice: content is not a file: /x/y`,
}, {
	summary: "Non-default archive",
	slices:  []setup.SliceKey{{"test-package", "myslice"}},
	release: map[string]string{
		"chisel.yaml": `
			format: chisel-v1
			archives:
				foo:
					version: 22.04
					components: [main, universe]
					default: true
					v1-public-keys: [test-key]
				bar:
					version: 22.04
					components: [main]
					v1-public-keys: [test-key]
			v1-public-keys:
				test-key:
					id: ` + testKey.ID + `
					armor: |` + "\n" + testutil.PrefixEachLine(testKey.PubKeyArmor, "\t\t\t\t\t\t") + `
		`,
		"slices/mydir/test-package.yaml": `
			package: test-package
			archive: bar
			slices:
				myslice:
					contents:
						/dir/nested/file:
		`,
	},
	fsResult: map[string]string{
		"/dir/":            "dir 0755",
		"/dir/nested/":     "dir 0755",
		"/dir/nested/file": "file 0644 84237a05",
	},
	reportResult: map[string]string{
		"/dir/nested/file": "file 0644 84237a05 {test-package_myslice}",
	},
}}

var defaultChiselYaml = `
	format: chisel-v1
	archives:
		ubuntu:
			version: 22.04
			components: [main, universe]
			v1-public-keys: [test-key]
	v1-public-keys:
		test-key:
			id: ` + testKey.ID + `
			armor: |` + "\n" + testutil.PrefixEachLine(testKey.PubKeyArmor, "\t\t\t\t\t\t") + `
`

type testArchive struct {
	options archive.Options
	pkgs    map[string][]byte
}

func (a *testArchive) Options() *archive.Options {
	return &a.options
}

func (a *testArchive) Fetch(pkg string) (io.ReadCloser, error) {
	if data, ok := a.pkgs[pkg]; ok {
		return io.NopCloser(bytes.NewBuffer(data)), nil
	}
	return nil, fmt.Errorf("attempted to open %q package", pkg)
}

func (a *testArchive) Exists(pkg string) bool {
	_, ok := a.pkgs[pkg]
	return ok
}

func (s *S) TestRun(c *C) {
	// Run tests for format chisel-v1.
	runSlicerTests(c, slicerTests)

	// Run tests for format v1.
	v1SlicerTests := make([]slicerTest, len(slicerTests))
	for i, t := range slicerTests {
		t.error = strings.Replace(t.error, "chisel-v1", "v1", -1)
		t.error = strings.Replace(t.error, "v1-public-keys", "public-keys", -1)
		m := map[string]string{}
		for k, v := range t.release {
			v = strings.Replace(v, "chisel-v1", "v1", -1)
			v = strings.Replace(v, "v1-public-keys", "public-keys", -1)
			m[k] = v
		}
		t.release = m
		v1SlicerTests[i] = t
	}
	runSlicerTests(c, v1SlicerTests)
}

func runSlicerTests(c *C, tests []slicerTest) {
	for _, test := range tests {
		c.Logf("Summary: %s", test.summary)

		if _, ok := test.release["chisel.yaml"]; !ok {
			test.release["chisel.yaml"] = string(defaultChiselYaml)
		}

		releaseDir := c.MkDir()
		for path, data := range test.release {
			fpath := filepath.Join(releaseDir, path)
			err := os.MkdirAll(filepath.Dir(fpath), 0755)
			c.Assert(err, IsNil)
			err = os.WriteFile(fpath, testutil.Reindent(data), 0644)
			c.Assert(err, IsNil)
		}

		release, err := setup.ReadRelease(releaseDir)
		c.Assert(err, IsNil)

		selection, err := setup.Select(release, test.slices)
		c.Assert(err, IsNil)

		pkgs := map[string][]byte{
			"test-package": testutil.PackageData["test-package"],
		}
		for name, entries := range packageEntries {
			deb, err := testutil.MakeDeb(entries)
			c.Assert(err, IsNil)
			pkgs[name] = deb
		}
		archives := map[string]archive.Archive{}
		for name, setupArchive := range release.Archives {
			archive := &testArchive{
				options: archive.Options{
					Label:      setupArchive.Name,
					Version:    setupArchive.Version,
					Suites:     setupArchive.Suites,
					Components: setupArchive.Components,
					Arch:       test.arch,
				},
				pkgs: pkgs,
			}
			archives[name] = archive
		}

		targetDir := c.MkDir()
		options := slicer.RunOptions{
			Selection: selection,
			Archives:  archives,
			TargetDir: targetDir,
		}
		if test.hackopt != nil {
			test.hackopt(c, &options)
		}
		report, err := slicer.Run(&options)
		if test.error == "" {
			c.Assert(err, IsNil)
		} else {
			c.Assert(err, ErrorMatches, test.error)
			continue
		}

		if test.fsResult != nil {
			result := make(map[string]string, len(copyrightEntries)+len(test.fsResult))
			for k, v := range copyrightEntries {
				result[k] = v
			}
			for k, v := range test.fsResult {
				result[k] = v
			}
			c.Assert(testutil.TreeDump(targetDir), DeepEquals, result)
		}

		if test.reportResult != nil {
			/* result := make(map[string]string, len(copyrightEntries)+len(test.reportResult))
			for k, v := range copyrightEntries {
				result[k] = v + " {test-package_myslice}"
			}
			for k, v := range test.reportResult {
				result[k] = v
			}
			c.Assert(treeDumpReport(report), DeepEquals, result)*/
			c.Assert(treeDumpReport(report), DeepEquals, test.reportResult)
		}
	}
}

// treeDumpReport returns the file information in the same format as
// [testutil.TreeDump] with the added slices that have installed each path.
func treeDumpReport(report *slicer.Report) map[string]string {
	result := make(map[string]string)
	for _, entry := range report.Entries {
		fperm := entry.Mode.Perm()
		if entry.Mode&fs.ModeSticky != 0 {
			fperm |= 01000
		}
		var fsDump string
		switch entry.Mode.Type() {
		case fs.ModeDir:
			fsDump = fmt.Sprintf("dir %#o", fperm)
		case fs.ModeSymlink:
			fsDump = fmt.Sprintf("symlink %s", entry.Link)
		case 0: // Regular
			if entry.Size == 0 {
				fsDump = fmt.Sprintf("file %#o empty", entry.Mode.Perm())
			} else {
				fsDump = fmt.Sprintf("file %#o %s", fperm, entry.Hash[:8])
			}
		default:
			panic(fmt.Errorf("unknown file type %d: %s", entry.Mode.Type(), entry.Path))
		}

		// append {slice1, ..., sliceN} to the end of the entry dump.
		slicesStr := make([]string, 0, len(entry.Slices))
		for slice := range entry.Slices {
			slicesStr = append(slicesStr, slice.String())
		}
		result[entry.Path] = fmt.Sprintf("%s {%s}", fsDump, strings.Join(slicesStr, ","))
	}
	return result
}
