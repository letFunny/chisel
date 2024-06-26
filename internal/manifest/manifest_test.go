package manifest_test

import (
	"os"
	"path"
	"strings"

	"github.com/klauspost/compress/zstd"
	. "gopkg.in/check.v1"

	"github.com/canonical/chisel/internal/manifest"
)

var manifestTests = []struct {
	summary string
	input   string
	mfest   *manifest.Manifest
	error   string
}{
	{
		summary: "All types",
		input: `
{"jsonwall":"1.0","schema":"1.0","count":13}
{"kind":"content","slice":"pkg1_manifest","path":"/manifest/manifest.wall"}
{"kind":"content","slice":"pkg1_myslice","path":"/dir/file"}
{"kind":"content","slice":"pkg1_myslice","path":"/dir/foo/bar/"}
{"kind":"content","slice":"pkg1_myslice","path":"/dir/link/file"}
{"kind":"content","slice":"pkg2_myotherslice","path":"/dir/foo/bar/"}
{"kind":"package","name":"pkg1","version":"v1","sha256":"hash1","arch":"arch1"}
{"kind":"package","name":"pkg2","version":"v2","sha256":"hash2","arch":"arch2"}
{"kind":"path","path":"/dir/file","mode":"0644","slices":["pkg1_myslice"],"sha256":"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855","final_sha256":"8067926c032c090867013d14fb0eb21ae858344f62ad07086fd32375845c91a6","size":21}
{"kind":"path","path":"/dir/foo/bar/","mode":"01777","slices":["pkg1_myslice","pkg2_myotherslice"]}
{"kind":"path","path":"/dir/link/file","mode":"0644","slices":["pkg1_myslice"],"link":"/dir/file"}
{"kind":"path","path":"/manifest/manifest.wall","mode":"0644","slices":["pkg1_manifest"]}
{"kind":"slice","name":"pkg1_manifest"}
{"kind":"slice","name":"pkg1_myslice"}
{"kind":"slice","name":"pkg2_myotherslice"}
`,
		mfest: &manifest.Manifest{
			Paths: []manifest.Path{
				{Kind: "path", Path: "/dir/file", Mode: "0644", Slices: []string{"pkg1_myslice"}, Hash: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", FinalHash: "8067926c032c090867013d14fb0eb21ae858344f62ad07086fd32375845c91a6", Size: 0x15, Link: ""},
				{Kind: "path", Path: "/dir/foo/bar/", Mode: "01777", Slices: []string{"pkg1_myslice", "pkg2_myotherslice"}, Hash: "", FinalHash: "", Size: 0x0, Link: ""},
				{Kind: "path", Path: "/dir/link/file", Mode: "0644", Slices: []string{"pkg1_myslice"}, Hash: "", FinalHash: "", Size: 0x0, Link: "/dir/file"},
				{Kind: "path", Path: "/manifest/manifest.wall", Mode: "0644", Slices: []string{"pkg1_manifest"}, Hash: "", FinalHash: "", Size: 0x0, Link: ""},
			},
			Contents: []manifest.Content{
				{Kind: "content", Slice: "pkg1_manifest", Path: "/manifest/manifest.wall"},
				{Kind: "content", Slice: "pkg1_myslice", Path: "/dir/file"},
				{Kind: "content", Slice: "pkg1_myslice", Path: "/dir/foo/bar/"},
				{Kind: "content", Slice: "pkg1_myslice", Path: "/dir/link/file"},
				{Kind: "content", Slice: "pkg2_myotherslice", Path: "/dir/foo/bar/"},
			},
			Packages: []manifest.Package{
				{Kind: "package", Name: "pkg1", Version: "v1", Digest: "hash1", Arch: "arch1"},
				{Kind: "package", Name: "pkg2", Version: "v2", Digest: "hash2", Arch: "arch2"},
			},
			Slices: []manifest.Slice{
				{Kind: "slice", Name: "pkg1_manifest"},
				{Kind: "slice", Name: "pkg1_myslice"},
				{Kind: "slice", Name: "pkg2_myotherslice"},
			},
		},
	}, {
		summary: "Slice not found",
		input: `
{"jsonwall":"1.0","schema":"1.0","count":1}
{"kind":"content","slice":"pkg1_manifest","path":"/manifest/manifest.wall"}
`,
		error: `cannot read manifest: invalid manifest: slice pkg1_manifest not found in slices`,
	}, {
		summary: "Package not found",
		input: `
{"jsonwall":"1.0","schema":"1.0","count":1}
{"kind":"slice","name":"pkg1_manifest"}
`,
		error: `cannot read manifest: invalid manifest: package "pkg1" not found in packages`,
	}, {
		summary: "Path not found",
		input: `
{"jsonwall":"1.0","schema":"1.0","count":1}
{"kind":"path","path":"/dir/","mode":"01777","slices":["pkg1_myslice"]}
`,
		error: `cannot read manifest: invalid manifest: path /dir/ not found in contents`,
	}, {
		summary: "Content and path have different slices",
		input: `
{"jsonwall":"1.0","schema":"1.0","count":3}
{"kind":"content","slice":"pkg1_myotherslice","path":"/dir/"}
{"kind":"package","name":"pkg1","version":"v1","sha256":"hash1","arch":"arch1"}
{"kind":"path","path":"/dir/","mode":"01777","slices":["pkg1_myslice"]}
{"kind":"slice","name":"pkg1_myotherslice"}
`,
		error: `cannot read manifest: invalid manifest: path /dir/ and content have diverging slices: \["pkg1_myslice"\] != \["pkg1_myotherslice"\]`,
	}}

func (s *S) TestRun(c *C) {
	for _, test := range manifestTests {
		c.Logf("Summary: %s", test.summary)

		tmpDir := c.MkDir()
		f, err := os.OpenFile(path.Join(tmpDir, "manifest.wall"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
		c.Assert(err, IsNil)
		w, err := zstd.NewWriter(f)
		c.Assert(err, IsNil)
		_, err = w.Write([]byte(strings.TrimSpace(test.input)))
		c.Assert(err, IsNil)
		w.Close()
		f.Close()

		mfest, err := manifest.Read(tmpDir, "manifest.wall")
		if test.error != "" {
			c.Assert(err, ErrorMatches, test.error)
			continue
		}
		c.Assert(err, IsNil)
		c.Assert(mfest, DeepEquals, test.mfest)
	}
}