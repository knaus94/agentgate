// Package update replaces the running binary with the latest GitHub release.
package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Version and Repo are set at build time with -ldflags.
var (
	Version = "dev"
	Repo    = "knaus94/claude-dev"
)

// The release endpoints; tests point them at a local server.
var (
	apiBase      = "https://api.github.com"
	downloadBase = "https://github.com"
)

var client = &http.Client{Timeout: 60 * time.Second}

func get(url string) ([]byte, error) {
	request, _ := http.NewRequest(http.MethodGet, url, nil)
	request.Header.Set("User-Agent", "claude-dev/"+Version)
	if token := os.Getenv("GITHUB_TOKEN"); token != "" && strings.HasPrefix(url, apiBase) {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, response.Status)
	}
	return io.ReadAll(io.LimitReader(response.Body, 200<<20))
}

// AssetName is the release archive for this platform.
func AssetName() string { return fmt.Sprintf("claude-dev_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH) }

// Latest returns the tag of the newest release.
func Latest() (string, error) {
	raw, err := get(apiBase + "/repos/" + Repo + "/releases/latest")
	if err != nil {
		return "", err
	}
	var release struct {
		Tag string `json:"tag_name"`
	}
	if err := json.Unmarshal(raw, &release); err != nil || release.Tag == "" {
		return "", errors.New("the release feed has no tag")
	}
	return release.Tag, nil
}

// Current reports whether tag is the running version.
func Current(tag string) bool {
	return strings.TrimPrefix(tag, "v") == strings.TrimPrefix(Version, "v")
}

// Apply downloads the release, verifies its checksum and atomically replaces the running binary.
// It returns the path of the new binary.
func Apply(tag string) (string, error) {
	target, err := os.Executable()
	if err != nil {
		return "", err
	}
	if target, err = filepath.EvalSymlinks(target); err != nil {
		return "", err
	}
	return target, ApplyTo(tag, target)
}

// ApplyTo installs the release binary at target. Nothing is written unless the checksum matches.
func ApplyTo(tag, target string) error {
	base := downloadBase + "/" + Repo + "/releases/download/" + tag + "/"
	sums, err := get(base + "checksums.txt")
	if err != nil {
		return err
	}
	want := ""
	for _, line := range strings.Split(string(sums), "\n") {
		if fields := strings.Fields(line); len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == AssetName() {
			want = fields[0]
		}
	}
	if want == "" {
		return fmt.Errorf("release %s has no build for %s/%s", tag, runtime.GOOS, runtime.GOARCH)
	}
	archive, err := get(base + AssetName())
	if err != nil {
		return err
	}
	if sum := sha256.Sum256(archive); hex.EncodeToString(sum[:]) != want {
		return errors.New("checksum mismatch; nothing was installed")
	}
	binary, err := extract(archive)
	if err != nil {
		return err
	}
	staged := target + ".new"
	if err := os.WriteFile(staged, binary, 0o755); err != nil {
		return fmt.Errorf("cannot write next to %s (%w); reinstall with the install script or use sudo", target, err)
	}
	if err := os.Rename(staged, target); err != nil {
		_ = os.Remove(staged)
		return err
	}
	return nil
}

func extract(archive []byte) ([]byte, error) {
	unzipped, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	reader := tar.NewReader(unzipped)
	for {
		header, err := reader.Next()
		if err != nil {
			return nil, errors.New("the archive has no claude-dev binary")
		}
		if filepath.Base(header.Name) == "claude-dev" && header.Typeflag == tar.TypeReg {
			return io.ReadAll(io.LimitReader(reader, 200<<20))
		}
	}
}
