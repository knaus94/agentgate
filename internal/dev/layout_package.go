package dev

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type packageManifest struct {
	Digest string            `json:"digest"`
	Files  map[string]string `json:"files"`
}

var packageRequiredFiles = []string{"frame.png", "data/spec.json", "data/nodes.json", "brief.md"}

func validPackagePath(name string) bool {
	if name == "" || filepath.IsAbs(name) || strings.Contains(name, "\\") {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func readPackageManifest(dir string) (packageManifest, error) {
	var manifest packageManifest
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return manifest, err
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return manifest, err
	}
	if manifest.Files == nil || manifest.Digest == "" {
		return manifest, errors.New("manifest needs a content digest and file inventory")
	}
	return manifest, nil
}

func packageContentDigest(files map[string]string) string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	lines := make([]string, 0, len(names))
	for _, name := range names {
		lines = append(lines, name+":"+files[name])
	}
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

func verifyDesignPackage(dir string) error {
	fail := func(err error) error {
		return fmt.Errorf("design package %q is incomplete or modified: %w; import it again with `agentgate figma pull`", dir, err)
	}
	manifest, err := readPackageManifest(dir)
	if err != nil {
		return fail(err)
	}
	for _, name := range packageRequiredFiles {
		if _, exists := manifest.Files[name]; !exists {
			return fail(fmt.Errorf("inventory omits %s", name))
		}
	}
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return fail(err)
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return fail(err)
	}
	for name, expected := range manifest.Files {
		if !validPackagePath(name) || len(expected) != sha256.Size*2 {
			return fail(fmt.Errorf("invalid inventory entry %q", name))
		}
		if decoded, err := hex.DecodeString(expected); err != nil || len(decoded) != sha256.Size || strings.ToLower(expected) != expected {
			return fail(fmt.Errorf("invalid digest for %s", name))
		}
		target := filepath.Join(root, filepath.FromSlash(name))
		info, err := os.Lstat(target)
		if err != nil {
			return fail(err)
		}
		if !info.Mode().IsRegular() {
			return fail(fmt.Errorf("%s must be a regular file", name))
		}
		real, err := filepath.EvalSymlinks(target)
		if err != nil {
			return fail(err)
		}
		relative, err := filepath.Rel(root, real)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fail(fmt.Errorf("%s resolves outside the package", name))
		}
		file, err := os.Open(target)
		if err != nil {
			return fail(err)
		}
		hash := sha256.New()
		_, readErr := io.Copy(hash, file)
		closeErr := file.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			return fail(err)
		}
		if hex.EncodeToString(hash.Sum(nil)) != expected {
			return fail(fmt.Errorf("content of %s changed", name))
		}
	}
	if packageContentDigest(manifest.Files) != manifest.Digest {
		return fail(errors.New("content digest does not match the inventory"))
	}
	return nil
}

// packageEvidence binds a review to bytes on disk, not a manifest's self-reported
// digest. Deleting an inventory or altering spec.json invalidates prior evidence
// even before the next layout command reports the import error.
func (d DesignRef) packageEvidence() map[string]string {
	out := map[string]string{"manifest.json": entry(filepath.Join(d.Package, "manifest.json"))}
	manifest, err := readPackageManifest(d.Package)
	if err != nil {
		out["invalid"] = err.Error()
	}
	for _, name := range packageRequiredFiles {
		out[name] = entry(filepath.Join(d.Package, filepath.FromSlash(name)))
	}
	for name := range manifest.Files {
		if validPackagePath(name) {
			out[name] = entry(filepath.Join(d.Package, filepath.FromSlash(name)))
		}
	}
	return out
}
