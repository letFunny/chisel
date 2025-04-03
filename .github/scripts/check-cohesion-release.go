package main

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/blakesmith/ar"
	"github.com/canonical/chisel/internal/archive"
	"github.com/canonical/chisel/internal/cache"
	"github.com/canonical/chisel/internal/setup"
	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
)

type RunOptions struct {
	releaseStr string
	arch       string
}

func run(options *RunOptions) error {
	release, err := obtainRelease(options.releaseStr)
	if err != nil {
		return err
	}

	archives := make(map[string]archive.Archive)
	for archiveName, archiveInfo := range release.Archives {
		openArchive, err := archive.Open(&archive.Options{
			Label:      archiveName,
			Version:    archiveInfo.Version,
			Arch:       options.arch,
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

	pkgArchive, err := selectPkgArchives(archives, release)
	if err != nil {
		return err
	}

	// Fetch all packages, using the selection order.
	packages := make(map[string]io.ReadSeekCloser)
	for pkgName, archive := range pkgArchive {
		reader, _, err := archive.Fetch(pkgName)
		if err != nil {
			return err
		}
		defer reader.Close()
		packages[pkgName] = reader
	}

	type ownership struct {
		mode int64
		link string
		pkgs []string
	}
	directories := map[string][]ownership{}
	for pkgName, pkgReader := range packages {
		dataReader, err := getDataReader(pkgReader)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "processing %s\n", pkgName)
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

			data, ok := directories[path]
			if !ok {
				o := ownership{
					mode: tarHeader.Mode,
					link: tarHeader.Linkname,
					pkgs: []string{pkgName},
				}
				directories[path] = []ownership{o}
			}

			found := false
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

	for dir, data := range directories {
		if len(data) == 1 {
			continue
		}
		fmt.Printf("%s:\n", dir)
		for _, o := range data {
			var pkgsStr string
			if len(o.pkgs) <= 3 {
				pkgsStr = fmt.Sprintf("%s", o.pkgs)
			} else {
				pkgsStr = fmt.Sprintf("[%s,%s,%s...(and %d more)]", o.pkgs[0], o.pkgs[1], o.pkgs[2], len(o.pkgs)-3)
			}
			fmt.Printf("    (mode: 0%o, link: %q, pkgs: %s)\n", o.mode, o.link, pkgsStr)
		}
	}

	return nil
}

func main() {
	release, ok := os.LookupEnv("RELEASE")
	if !ok {
		release = "ubuntu-24.04"
	}
	arch, ok := os.LookupEnv("ARCH")
	if !ok {
		arch = "amd64"
	}

	options := &RunOptions{
		releaseStr: release,
		arch:       arch,
	}
	err := run(options)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err)
		os.Exit(1)
	}
}

// selectPkgArchives selects the highest priority archive containing the package
// unless a particular archive is pinned within the slice definition file. It
// returns a map of archives indexed by package names.
func selectPkgArchives(archives map[string]archive.Archive, release *setup.Release) (map[string]archive.Archive, error) {
	sortedArchives := make([]*setup.Archive, 0, len(release.Archives))
	for _, archive := range release.Archives {
		if archive.Priority < 0 {
			// Ignore negative priority archives unless a package specifically
			// asks for it with the "archive" field.
			continue
		}
		sortedArchives = append(sortedArchives, archive)
	}
	slices.SortFunc(sortedArchives, func(a, b *setup.Archive) int {
		return b.Priority - a.Priority
	})

	pkgArchive := make(map[string]archive.Archive)
	for _, pkg := range release.Packages {
		var candidates []*setup.Archive
		if pkg.Archive == "" {
			// If the package has not pinned any archive, choose the highest
			// priority archive in which the package exists.
			candidates = sortedArchives
		} else {
			candidates = []*setup.Archive{release.Archives[pkg.Archive]}
		}

		var chosen archive.Archive
		for _, archiveInfo := range candidates {
			archive := archives[archiveInfo.Name]
			if archive != nil && archive.Exists(pkg.Name) {
				chosen = archive
				break
			}
		}
		if chosen == nil {
			// return nil, fmt.Errorf("cannot find package %q in archive(s)", pkg.Name)
			// TODO we need to continue instead of returning because in some
			// architectures the package is not present and we want to skip it
			// and proceed.
			continue
		}
		pkgArchive[pkg.Name] = chosen
	}
	return pkgArchive, nil
}

func getDataReader(pkgReader io.ReadSeeker) (io.ReadCloser, error) {
	arReader := ar.NewReader(pkgReader)
	var dataReader io.ReadCloser
	for dataReader == nil {
		arHeader, err := arReader.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("no data payload")
		}
		if err != nil {
			return nil, err
		}
		switch arHeader.Name {
		case "data.tar.gz":
			gzipReader, err := gzip.NewReader(arReader)
			if err != nil {
				return nil, err
			}
			dataReader = gzipReader
		case "data.tar.xz":
			xzReader, err := xz.NewReader(arReader)
			if err != nil {
				return nil, err
			}
			dataReader = io.NopCloser(xzReader)
		case "data.tar.zst":
			zstdReader, err := zstd.NewReader(arReader)
			if err != nil {
				return nil, err
			}
			dataReader = zstdReader.IOReadCloser()
		}
	}

	return dataReader, nil
}

// obtainRelease returns the Chisel release information matching the provided string,
// fetching it if necessary. The provided string should be either:
// * "<name>-<version>",
// * the path to a directory containing a previously fetched release,
// * "" and Chisel will attempt to read the release label from the host.
func obtainRelease(releaseStr string) (release *setup.Release, err error) {
	if strings.Contains(releaseStr, "/") {
		release, err = setup.ReadRelease(releaseStr)
	} else {
		var label, version string
		if releaseStr == "" {
			label, version, err = readReleaseInfo()
		} else {
			label, version, err = parseReleaseInfo(releaseStr)
		}
		if err != nil {
			return nil, err
		}
		release, err = setup.FetchRelease(&setup.FetchOptions{
			Label:   label,
			Version: version,
		})
	}
	if err != nil {
		return nil, err
	}
	return release, nil
}

var releaseExp = regexp.MustCompile(`^([a-z](?:-?[a-z0-9]){2,})-([0-9]+(?:\.?[0-9])+)$`)

func parseReleaseInfo(release string) (label, version string, err error) {
	match := releaseExp.FindStringSubmatch(release)
	if match == nil {
		return "", "", fmt.Errorf("invalid release reference: %q", release)
	}
	return match[1], match[2], nil
}

func readReleaseInfo() (label, version string, err error) {
	data, err := os.ReadFile("/etc/lsb-release")
	if err == nil {
		const labelPrefix = "DISTRIB_ID="
		const versionPrefix = "DISTRIB_RELEASE="
		for _, line := range strings.Split(string(data), "\n") {
			switch {
			case strings.HasPrefix(line, labelPrefix):
				label = strings.ToLower(line[len(labelPrefix):])
			case strings.HasPrefix(line, versionPrefix):
				version = line[len(versionPrefix):]
			}
			if label != "" && version != "" {
				return label, version, nil
			}
		}
	}
	return "", "", fmt.Errorf("cannot infer release via /etc/lsb-release, see the --release option")
}

// sanitizeTarPath removes the leading "./" from the source path in the tarball,
// and verifies that the path is not empty.
func sanitizeTarPath(path string) (string, bool) {
	if len(path) < 3 || path[0] != '.' || path[1] != '/' {
		return "", false
	}
	return path[1:], true
}
