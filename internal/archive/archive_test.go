package archive_test

import (
	"golang.org/x/crypto/openpgp/packet"
	. "gopkg.in/check.v1"

	"debug/elf"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/canonical/chisel/internal/archive"
	"github.com/canonical/chisel/internal/archive/testarchive"
	"github.com/canonical/chisel/internal/deb"
	"github.com/canonical/chisel/internal/testutil"
)

type httpSuite struct {
	logf      func(string, ...interface{})
	base      string
	request   *http.Request
	requests  []*http.Request
	response  string
	responses map[string][]byte
	err       error
	header    http.Header
	status    int
	restore   func()
	privKey   *packet.PrivateKey
	pubKey    *packet.PublicKey
}

var _ = Suite(&httpSuite{})

var (
	key1          = testutil.PGPKeys["key1"]
	key2          = testutil.PGPKeys["key2"]
	keyUbuntu2018 = testutil.PGPKeys["key-ubuntu-2018"]
)

func (s *httpSuite) SetUpTest(c *C) {
	s.logf = c.Logf
	s.err = nil
	s.base = "http://archive.ubuntu.com/ubuntu/"
	s.request = nil
	s.requests = nil
	s.response = ""
	s.responses = make(map[string][]byte)
	s.header = nil
	s.status = 200
	s.restore = archive.FakeDo(s.Do)
	s.privKey = key1.PrivKey
	s.pubKey = key1.PubKey
}

func (s *httpSuite) TearDownTest(c *C) {
	s.restore()
}

func (s *httpSuite) Do(req *http.Request) (*http.Response, error) {
	if s.base != "" && !strings.HasPrefix(req.URL.String(), s.base) {
		return nil, fmt.Errorf("test expected base %q, got %q", s.base, req.URL.String())
	}

	s.request = req
	s.requests = append(s.requests, req)
	body := s.response
	s.logf("Request: %s", req.URL.String())
	if response, ok := s.responses[path.Clean(req.URL.Path)]; ok {
		body = string(response)
	}
	rsp := &http.Response{
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     s.header,
		StatusCode: s.status,
	}
	return rsp, s.err
}

func (s *httpSuite) TestDoError(c *C) {
	s.err = errors.New("BAM")

	options := archive.Options{
		Label:      "ubuntu",
		Version:    "22.04",
		Arch:       "amd64",
		Suites:     []string{"jammy"},
		Components: []string{"main"},
		CacheDir:   c.MkDir(),
	}

	_, err := archive.Open(&options)
	c.Check(err, ErrorMatches, "cannot talk to archive: BAM")
}

func (s *httpSuite) prepareArchive(suite, version, arch string, components []string) *testarchive.Release {
	return s.prepareArchiveAdjustRelease(suite, version, arch, components, nil)
}

func (s *httpSuite) prepareArchiveAdjustRelease(suite, version, arch string, components []string, adjustRelease func(*testarchive.Release)) *testarchive.Release {
	release := &testarchive.Release{
		Suite:   suite,
		Version: version,
		Label:   "Ubuntu",
		PrivKey: s.privKey,
	}
	for i, component := range components {
		index := &testarchive.PackageIndex{
			Component: component,
			Arch:      arch,
		}
		for j := 0; j < 2; j++ {
			seq := 1 + i*2 + j
			index.Packages = append(index.Packages, &testarchive.Package{
				Name:      fmt.Sprintf("mypkg%d", seq),
				Version:   fmt.Sprintf("1.%d", seq),
				Arch:      arch,
				Component: component,
			})
		}
		release.Items = append(release.Items, index)
		release.Items = append(release.Items, &testarchive.Gzip{index})
	}
	base, err := url.Parse(s.base)
	if err != nil {
		panic(err)
	}
	if adjustRelease != nil {
		adjustRelease(release)
	}
	release.Render(base.Path, s.responses)
	return release
}

type optionErrorTest struct {
	options archive.Options
	error   string
}

var optionErrorTests = []optionErrorTest{{
	options: archive.Options{
		Label:      "ubuntu",
		Version:    "22.04",
		Arch:       "amd64",
		Suites:     []string{"jammy"},
		Components: []string{"main", "other"},
	},
	error: `archive has no component "other"`,
}, {
	options: archive.Options{
		Label:   "ubuntu",
		Version: "22.04",
		Arch:    "amd64",
		Suites:  []string{"jammy"},
	},
	error: "archive options missing components",
}, {
	options: archive.Options{
		Label:      "ubuntu",
		Version:    "22.04",
		Arch:       "amd64",
		Components: []string{"main", "other"},
	},
	error: `archive options missing suites`,
}, {
	options: archive.Options{
		Label:      "ubuntu",
		Version:    "22.04",
		Arch:       "foo",
		Suites:     []string{"jammy"},
		Components: []string{"main", "other"},
	},
	error: `invalid package architecture: foo`,
}}

func (s *httpSuite) TestOptionErrors(c *C) {
	s.prepareArchive("jammy", "22.04", "arm64", []string{"main", "universe"})
	cacheDir := c.MkDir()
	for _, test := range optionErrorTests {
		test.options.CacheDir = cacheDir
		test.options.PubKeys = append(test.options.PubKeys, s.pubKey)
		_, err := archive.Open(&test.options)
		c.Assert(err, ErrorMatches, test.error)
	}
}

func (s *httpSuite) TestFetchPackage(c *C) {

	s.prepareArchive("jammy", "22.04", "amd64", []string{"main", "universe"})

	options := archive.Options{
		Label:      "ubuntu",
		Version:    "22.04",
		Arch:       "amd64",
		Suites:     []string{"jammy"},
		Components: []string{"main", "universe"},
		CacheDir:   c.MkDir(),
		PubKeys:    []*packet.PublicKey{s.pubKey},
	}

	testArchive, err := archive.Open(&options)
	c.Assert(err, IsNil)

	// First on component main.
	pkg, info, err := testArchive.Fetch("mypkg1")
	c.Assert(err, IsNil)
	c.Assert(info, DeepEquals, &archive.PackageInfo{
		Name:    "mypkg1",
		Version: "1.1",
		Arch:    "amd64",
		SHA256:  "1f08ef04cfe7a8087ee38a1ea35fa1810246648136c3c42d5a61ad6503d85e05",
	})
	c.Assert(read(pkg), Equals, "mypkg1 1.1 data")

	// Last on component universe.
	pkg, info, err = testArchive.Fetch("mypkg4")
	c.Assert(err, IsNil)
	c.Assert(info, DeepEquals, &archive.PackageInfo{
		Name:    "mypkg4",
		Version: "1.4",
		Arch:    "amd64",
		SHA256:  "54af70097b30b33cfcbb6911ad3d0df86c2d458928169e348fa7873e4fc678e4",
	})
	c.Assert(read(pkg), Equals, "mypkg4 1.4 data")
}

func (s *httpSuite) TestFetchPortsPackage(c *C) {

	s.base = "http://ports.ubuntu.com/ubuntu-ports/"

	s.prepareArchive("jammy", "22.04", "arm64", []string{"main", "universe"})

	options := archive.Options{
		Label:      "ubuntu",
		Version:    "22.04",
		Arch:       "arm64",
		Suites:     []string{"jammy"},
		Components: []string{"main", "universe"},
		CacheDir:   c.MkDir(),
		PubKeys:    []*packet.PublicKey{s.pubKey},
	}

	testArchive, err := archive.Open(&options)
	c.Assert(err, IsNil)

	// First on component main.
	pkg, info, err := testArchive.Fetch("mypkg1")
	c.Assert(err, IsNil)
	c.Assert(info, DeepEquals, &archive.PackageInfo{
		Name:    "mypkg1",
		Version: "1.1",
		Arch:    "arm64",
		SHA256:  "1f08ef04cfe7a8087ee38a1ea35fa1810246648136c3c42d5a61ad6503d85e05",
	})
	c.Assert(read(pkg), Equals, "mypkg1 1.1 data")

	// Last on component universe.
	pkg, info, err = testArchive.Fetch("mypkg4")
	c.Assert(err, IsNil)
	c.Assert(info, DeepEquals, &archive.PackageInfo{
		Name:    "mypkg4",
		Version: "1.4",
		Arch:    "arm64",
		SHA256:  "54af70097b30b33cfcbb6911ad3d0df86c2d458928169e348fa7873e4fc678e4",
	})
	c.Assert(read(pkg), Equals, "mypkg4 1.4 data")
}

func (s *httpSuite) TestFetchSecurityPackage(c *C) {

	for i, suite := range []string{"jammy", "jammy-updates", "jammy-security"} {
		release := s.prepareArchive(suite, "22.04", "amd64", []string{"main", "universe"})
		release.Walk(func(item testarchive.Item) error {
			if p, ok := item.(*testarchive.Package); ok && p.Name == "mypkg1" {
				p.Version = fmt.Sprintf("%s.%d", p.Version, i)
				p.Data = []byte("package from " + suite)
			}
			return nil
		})
		release.Render("/ubuntu", s.responses)
	}

	options := archive.Options{
		Label:      "ubuntu",
		Version:    "22.04",
		CacheDir:   c.MkDir(),
		Arch:       "amd64",
		Suites:     []string{"jammy", "jammy-security", "jammy-updates"},
		Components: []string{"main", "universe"},
		PubKeys:    []*packet.PublicKey{s.pubKey},
	}

	testArchive, err := archive.Open(&options)
	c.Assert(err, IsNil)

	pkg, info, err := testArchive.Fetch("mypkg1")
	c.Assert(err, IsNil)
	c.Assert(info, DeepEquals, &archive.PackageInfo{
		Name:    "mypkg1",
		Version: "1.1.2.2",
		Arch:    "amd64",
		SHA256:  "5448585bdd916e5023eff2bc1bc3b30bcc6ee9db9c03e531375a6a11ddf0913c",
	})
	c.Assert(read(pkg), Equals, "package from jammy-security")

	pkg, info, err = testArchive.Fetch("mypkg2")
	c.Assert(err, IsNil)
	c.Assert(info, DeepEquals, &archive.PackageInfo{
		Name:    "mypkg2",
		Version: "1.2",
		Arch:    "amd64",
		SHA256:  "a4b4f3f3a8fa09b69e3ba23c60a41a1f8144691fd371a2455812572fd02e6f79",
	})
	c.Assert(read(pkg), Equals, "mypkg2 1.2 data")
}

func (s *httpSuite) TestArchiveLabels(c *C) {
	setLabel := func(label string) func(*testarchive.Release) {
		return func(r *testarchive.Release) {
			r.Label = label
		}
	}

	s.prepareArchive("jammy", "22.04", "amd64", []string{"main", "universe"})

	options := archive.Options{
		Label:      "ubuntu",
		Version:    "22.04",
		Arch:       "amd64",
		Suites:     []string{"jammy"},
		Components: []string{"main", "universe"},
		CacheDir:   c.MkDir(),
		PubKeys:    []*packet.PublicKey{s.pubKey},
	}

	_, err := archive.Open(&options)
	c.Assert(err, IsNil)

	s.prepareArchiveAdjustRelease("jammy", "22.04", "amd64", []string{"main", "universe"}, setLabel("Ubuntu"))

	options = archive.Options{
		Label:      "ubuntu",
		Version:    "22.04",
		Arch:       "amd64",
		Suites:     []string{"jammy"},
		Components: []string{"main", "universe"},
		CacheDir:   c.MkDir(),
		PubKeys:    []*packet.PublicKey{s.pubKey},
	}

	_, err = archive.Open(&options)
	c.Assert(err, IsNil)

	s.prepareArchiveAdjustRelease("jammy", "22.04", "amd64", []string{"main", "universe"}, setLabel("UbuntuProFIPS"))

	options = archive.Options{
		Label:      "ubuntu",
		Version:    "22.04",
		Arch:       "amd64",
		Suites:     []string{"jammy"},
		Components: []string{"main", "universe"},
		CacheDir:   c.MkDir(),
		PubKeys:    []*packet.PublicKey{s.pubKey},
	}

	_, err = archive.Open(&options)
	c.Assert(err, IsNil)

	s.prepareArchiveAdjustRelease("jammy", "22.04", "amd64", []string{"main", "universe"}, setLabel("ThirdParty"))

	options = archive.Options{
		Label:      "ubuntu",
		Version:    "22.04",
		Arch:       "amd64",
		Suites:     []string{"jammy"},
		Components: []string{"main", "universe"},
		CacheDir:   c.MkDir(),
		PubKeys:    []*packet.PublicKey{s.pubKey},
	}

	_, err = archive.Open(&options)
	c.Assert(err, ErrorMatches, `.*\bno Ubuntu section`)
}

type verifyArchiveReleaseTest struct {
	summary string
	pubKeys []*packet.PublicKey
	error   string
}

var verifyArchiveReleaseTests = []verifyArchiveReleaseTest{{
	summary: "A valid public key",
	pubKeys: []*packet.PublicKey{key1.PubKey},
}, {
	summary: "No public key to verify with",
	error:   `cannot verify signature of the InRelease file`,
}, {
	summary: "Wrong public key",
	pubKeys: []*packet.PublicKey{key2.PubKey},
	error:   `cannot verify signature of the InRelease file`,
}, {
	summary: "Multiple public keys (invalid, valid)",
	pubKeys: []*packet.PublicKey{key2.PubKey, key1.PubKey},
}}

func (s *httpSuite) TestVerifyArchiveRelease(c *C) {
	for _, test := range verifyArchiveReleaseTests {
		c.Logf("Summary: %s", test.summary)

		s.prepareArchive("jammy", "22.04", "amd64", []string{"main", "universe"})

		options := archive.Options{
			Label:      "ubuntu",
			Version:    "22.04",
			Arch:       "amd64",
			Suites:     []string{"jammy"},
			Components: []string{"main", "universe"},
			CacheDir:   c.MkDir(),
			PubKeys:    test.pubKeys,
		}

		_, err := archive.Open(&options)
		if test.error != "" {
			c.Assert(err, ErrorMatches, test.error)
		} else {
			c.Assert(err, IsNil)
		}
	}
}

var packageInfoTests = []struct {
	summary string
	pkg     string
	info    *archive.PackageInfo
	error   string
}{{
	summary: "Basic",
	pkg:     "mypkg1",
	info: &archive.PackageInfo{
		Name:    "mypkg1",
		Version: "1.1",
		Arch:    "amd64",
		SHA256:  "1f08ef04cfe7a8087ee38a1ea35fa1810246648136c3c42d5a61ad6503d85e05",
	},
}, {
	summary: "Package not found in archive",
	pkg:     "mypkg99",
	error:   `cannot find package "mypkg99" in archive`,
}}

func (s *httpSuite) TestPackageInfo(c *C) {
	s.prepareArchive("jammy", "22.04", "amd64", []string{"main", "universe"})

	options := archive.Options{
		Label:      "ubuntu",
		Version:    "22.04",
		Arch:       "amd64",
		Suites:     []string{"jammy"},
		Components: []string{"main", "universe"},
		CacheDir:   c.MkDir(),
		PubKeys:    []*packet.PublicKey{s.pubKey},
	}

	testArchive, err := archive.Open(&options)
	c.Assert(err, IsNil)

	for _, test := range packageInfoTests {
		info, err := testArchive.Info(test.pkg)
		if test.error != "" {
			c.Assert(err, ErrorMatches, test.error)
			continue
		}
		c.Assert(err, IsNil)
		c.Assert(info, DeepEquals, test.info)
	}
}

func read(r io.Reader) string {
	data, err := io.ReadAll(r)
	if err != nil {
		panic(err)
	}
	return string(data)
}

// ----------------------------------------------------------------------------------------
// Real archive tests, only enabled via --real-archive.

var realArchiveFlag = flag.Bool("real-archive", false, "Perform tests against real archive")

func (s *S) TestRealArchive(c *C) {
	if !*realArchiveFlag {
		c.Skip("--real-archive not provided")
	}
	for _, release := range ubuntuReleases {
		for _, arch := range elfToDebArch {
			s.testOpenArchiveArch(c, release, arch)
		}
	}
}

type ubuntuRelease struct {
	name           string
	version        string
	archivePubKeys []*packet.PublicKey
}

var ubuntuReleases = []ubuntuRelease{{
	name:    "focal",
	version: "20.04",
	archivePubKeys: []*packet.PublicKey{
		keyUbuntu2018.PubKey,
	},
}, {
	name:    "jammy",
	version: "22.04",
	archivePubKeys: []*packet.PublicKey{
		keyUbuntu2018.PubKey,
	},
}, {
	name:    "noble",
	version: "24.04",
	archivePubKeys: []*packet.PublicKey{
		keyUbuntu2018.PubKey,
	},
}}

var elfToDebArch = map[elf.Machine]string{
	elf.EM_386:     "i386",
	elf.EM_AARCH64: "arm64",
	elf.EM_ARM:     "armhf",
	elf.EM_PPC64:   "ppc64el",
	elf.EM_RISCV:   "riscv64",
	elf.EM_S390:    "s390x",
	elf.EM_X86_64:  "amd64",
}

func (s *S) checkArchitecture(c *C, arch string, binaryPath string) {
	file, err := elf.Open(binaryPath)
	c.Assert(err, IsNil)
	defer file.Close()

	binaryArch := elfToDebArch[file.Machine]
	c.Assert(binaryArch, Equals, arch)
}

func (s *S) testOpenArchiveArch(c *C, release ubuntuRelease, arch string) {
	c.Logf("Checking ubuntu archive %s %s...", release.name, arch)

	options := archive.Options{
		Label:      "ubuntu",
		Version:    release.version,
		Arch:       arch,
		Suites:     []string{release.name},
		Components: []string{"main", "universe"},
		CacheDir:   c.MkDir(),
		PubKeys:    release.archivePubKeys,
	}

	testArchive, err := archive.Open(&options)
	c.Assert(err, IsNil)

	extractDir := c.MkDir()

	pkg, info, err := testArchive.Fetch("hostname")
	c.Assert(err, IsNil)
	c.Assert(info.Name, DeepEquals, "hostname")
	c.Assert(info.Arch, DeepEquals, arch)

	err = deb.Extract(pkg, &deb.ExtractOptions{
		Package:   "hostname",
		TargetDir: extractDir,
		Extract: map[string][]deb.ExtractInfo{
			"/usr/share/doc/hostname/copyright": {
				{Path: "/copyright"},
			},
			"/bin/hostname": {
				{Path: "/hostname"},
			},
		},
	})
	c.Assert(err, IsNil)

	data, err := os.ReadFile(filepath.Join(extractDir, "copyright"))
	c.Assert(err, IsNil)

	copyrightTop := "This package was written by Peter Tobias <tobias@et-inf.fho-emden.de>"
	c.Assert(strings.Contains(string(data), copyrightTop), Equals, true)

	s.checkArchitecture(c, arch, filepath.Join(extractDir, "hostname"))
}
