package main

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/google/go-containerregistry/pkg/crane"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	rpmdb "github.com/knqyf263/go-rpmdb/pkg"
)

const helptext = "Searches an image's layers for the first layer containing an RPMDB, builds a list of files installed, then checks subsequent layers for modifications to those files"

func main() {
	if len(os.Args) != 2 {
		fmt.Println("This only takes a single container reference as an argument. E.g. quay.io/mynamespace/myimage@digest")
		fmt.Println(helptext)
		os.Exit(10)
	}
	testContainer := os.Args[1]
	fmt.Println("Container under test:", testContainer)

	myImg, err := crane.Pull(testContainer)
	mne(err, "pull img")
	layers, err := myImg.Layers()
	mne(err, "get layers")

	found, layerIndex, packages := FindRPMDB(layers)
	if !found {
		panic(errors.New("unable to find valid RPMDB in any layer of the image"))
	}

	if layerIndex == len(layers)-1 {
		fmt.Println("The layer that contained the rpmdb was the last layer, so we consider it not possible to modify files. this is a pass case.")
		os.Exit(0)
	}

	filemap, err := InstalledFileMap(packages)
	mne(err, "couldn't extract a filemap from the package list")

	if len(filemap) == 0 {
		panic(errors.New("filemap was empty"))
	}

	remainingLayers := layers[layerIndex+1:]
	disallowedModifications := map[string]string{}
	for _, layer := range remainingLayers {
		id, _ := layer.Digest()
		fmt.Println("Checking layer for disallowed modifications", id)
		modifiedFiles, err := GenerateChangesFor(layer)
		mne(err, "error getting files from remaining layer")
		var modFound bool
		for _, modifiedFile := range modifiedFiles {
			if _, found := filemap[modifiedFile]; found && (!DirectoryIsExcluded(modifiedFile) && !FileIsExcluded(modifiedFile)) {
				modFound = true
				disallowedModifications[modifiedFile] = id.String()
			}
		}
		if modFound {
			fmt.Println(red("\tfound disallowed modification in layer"))
		}
		b, _ := json.MarshalIndent(modifiedFiles, "", "    ")
		os.WriteFile(fmt.Sprintf("modified-in-%s.json", id.String()), b, 0644)
	}
	b, _ := json.MarshalIndent(filemap, "", "    ")
	os.WriteFile("filemap.json", b, 0644)
	b, _ = json.MarshalIndent(disallowedModifications, "", "    ")
	os.WriteFile("disallowedmods.json", b, 0644)
}

// FindRPMDB attempts to extract a valid RPMDB from layers in the order
// they are provided. If found is not set to true, foundIndex and pkglist should
// be disregarded as any value there will be invalid.
func FindRPMDB(layers []v1.Layer) (found bool, foundIndex int, pkglist []*rpmdb.PackageInfo) {
	for i, layer := range layers {
		var err error
		pkglist, err = ExtractRPMDB(layer)
		if err == nil {
			id, _ := layer.Digest()
			fmt.Println("layer", id, "contained the rpmdb")
			found = true
			foundIndex = i
			return
		}
	}

	return found, foundIndex, pkglist
}

func DirectoryIsExcluded(s string) bool {
	excl := map[string]struct{}{
		"etc": {},
		"var": {},
		"run": {},
	}

	for k, _ := range excl {
		if strings.HasPrefix(s, filepath.Clean(k+"/")) || k == s {
			fmt.Println("\t", s, "was excluded by", yellow("directory"), "exclusions")
			return true
		}
	}

	return false
}

func FileIsExcluded(s string) bool {
	excl := map[string]struct{}{
		"etc/resolv.conf": {},
		"etc/hostname":    {},
	}

	_, found := excl[s]
	if found {
		fmt.Println("\t", s, "was excluded by", blue("file"), "exclusions")
	}
	return found
}

// Normalize will clean a filepath of extraneous characters like ./, //, etc.
// and strip a leading slash. E.g. /foo/../baz --> foo/baz
func Normalize(s string) string {
	return filepath.Clean(strings.TrimPrefix(s, "/"))
}

// InstalledFileMap gets a map of installed filenames that have been cleaned
// of extra slashes, dotslashes, and leading slashes.
func InstalledFileMap(pkglist []*rpmdb.PackageInfo) (map[string]string, error) {
	m := map[string]string{}
	for _, pkg := range pkglist {
		files, err := pkg.InstalledFileNames()
		if err != nil {
			return m, err
		}

		for _, file := range files {
			m[strings.TrimPrefix(filepath.Clean(file), "/")] = fmt.Sprintf("%s-%s", pkg.Name, pkg.Version)
		}
	}
	return m, nil
}

const whiteoutPrefix = ".wh."

// GenerateChangesFor will check layer for file changes, and will return a list of those.
func GenerateChangesFor(layer v1.Layer) ([]string, error) {
	layerReader, err := layer.Uncompressed()
	if err != nil {
		return nil, fmt.Errorf("reading layer contents: %w", err)
	}
	defer layerReader.Close()
	tarReader := tar.NewReader(layerReader)
	var filelist []string
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading tar: %w", err)
		}

		// Some tools prepend everything with "./", so if we don't Clean the
		// name, we may have duplicate entries, which angers tar-split.
		header.Name = filepath.Clean(header.Name)
		// force PAX format to remove Name/Linkname length limit of 100 characters
		// required by USTAR and to not depend on internal tar package guess which
		// prefers USTAR over PAX
		header.Format = tar.FormatPAX

		basename := filepath.Base(header.Name)
		dirname := filepath.Dir(header.Name)
		tombstone := strings.HasPrefix(basename, whiteoutPrefix)
		if tombstone {
			basename = basename[len(whiteoutPrefix):]
		}
		switch {
		case (header.Typeflag == tar.TypeDir && tombstone) || header.Typeflag == tar.TypeReg:
			filelist = append(filelist, strings.TrimPrefix(filepath.Join(dirname, basename), "/"))
		case header.Typeflag == tar.TypeSymlink:
			filelist = append(filelist, strings.TrimPrefix(header.Linkname, "/"))
		default:
			// TODO: what do we do with other flags?
			continue
		}
	}

	return filelist, nil
}

// ExtractRPMDB copies /var/lib/rpm/* from the archive and derives a list of packages from
// the rpm database.
func ExtractRPMDB(layer v1.Layer) ([]*rpmdb.PackageInfo, error) {
	layerReader, err := layer.Uncompressed()
	if err != nil {
		return nil, fmt.Errorf("reading layer contents: %w", err)
	}
	defer layerReader.Close()

	basepath, err := os.MkdirTemp("", "rpmdb-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(basepath)

	tarReader := tar.NewReader(layerReader)
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading tar: %w", err)
		}

		// Some tools prepend everything with "./", so if we don't Clean the
		// name, we may have duplicate entries, which angers tar-split.
		header.Name = filepath.Clean(header.Name)
		header.Format = tar.FormatPAX
		rpmdirname := filepath.Clean("var/lib/rpm")
		basename := filepath.Base(header.Name)
		dirname := filepath.Dir(header.Name)
		tombstone := strings.HasPrefix(basename, whiteoutPrefix)

		// a dir or file with the correct var/lib/rpm prefix that has not been marked with a tombstone is valid.
		if (header.Typeflag == tar.TypeDir || header.Typeflag == tar.TypeReg) && strings.HasPrefix(filepath.Join(dirname, basename), rpmdirname) && !tombstone {
			if header.Typeflag == tar.TypeDir {
				err := os.MkdirAll(filepath.Join(basepath, dirname, basename), header.FileInfo().Mode())
				if err != nil {
					return nil, err
				}
				continue
			}

			f, err := os.OpenFile(filepath.Join(basepath, dirname, basename), os.O_RDWR|os.O_CREATE|os.O_TRUNC, header.FileInfo().Mode())
			if err != nil {
				return nil, err
			}
			err = func() error {
				// closure here allows us to defer f.Close() in this iteration instead of
				// waiting for the parent function to complete.
				defer f.Close()
				_, err := io.Copy(f, tarReader)
				if err != nil {
					return err
				}
				return nil
			}()
			if err != nil {
				return nil, nil // TODO: is this correct to return nil here?
			}
		}
	}

	packageList, err := GetPackageList(context.TODO(), basepath)
	if err != nil {
		return nil, err
	}

	return packageList, nil
}

// GetPackageList returns the list of packages in the rpm database from either
// /var/lib/rpm/rpmdb.sqlite, or /var/lib/rpm/Packages if the former does not exist.
// If neither exists, this returns an error of type os.ErrNotExists
func GetPackageList(ctx context.Context, basePath string) ([]*rpmdb.PackageInfo, error) {
	rpmdirPath := filepath.Join(basePath, "var", "lib", "rpm")
	rpmdbPath := filepath.Join(rpmdirPath, "rpmdb.sqlite")

	if _, err := os.Stat(rpmdbPath); errors.Is(err, os.ErrNotExist) {
		// rpmdb.sqlite doesn't exist. Fall back to Packages
		rpmdbPath = filepath.Join(rpmdirPath, "Packages")

		// if the fall back path does not exist - this probably isn't a RHEL or UBI based image
		if _, err := os.Stat(rpmdbPath); errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}

	db, err := rpmdb.Open(rpmdbPath)
	if err != nil {
		return nil, fmt.Errorf("could not open rpm db: %v", err)
	}
	pkgList, err := db.ListPackages()
	if err != nil {
		return nil, fmt.Errorf("could not list packages: %v", err)
	}

	return pkgList, nil
}

var red = lipgloss.NewStyle().Foreground(lipgloss.Color("#D21404")).Render
var yellow = lipgloss.NewStyle().Foreground(lipgloss.Color("#D6B85A")).Render
var blue = lipgloss.NewStyle().Foreground(lipgloss.Color("#0000FF")).Render
