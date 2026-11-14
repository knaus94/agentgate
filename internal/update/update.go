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
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// Version and Repo are set at build time with -ldflags.
var (
	Version = "dev"
	Repo    = "knaus94/agentgate"
)

// The release endpoints; tests point them at a local server.
var (
	apiBase      = "https://api.github.com"
	downloadBase = "https://github.com"
)

var client = &http.Client{Timeout: 60 * time.Second}

const maxDownloadSize = 200 << 20

var safeTag = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._+-]*$`)

func get(url string) ([]byte, error) {
	return getLimited(url, maxDownloadSize)
}

func getLimited(address string, limit int64) ([]byte, error) {
	request, err := http.NewRequest(http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "agentgate/"+Version)
	apiURL, err := url.Parse(apiBase)
	if err != nil {
		return nil, err
	}
	if token := os.Getenv("GITHUB_TOKEN"); token != "" && request.URL.Scheme == apiURL.Scheme && request.URL.Host == apiURL.Host {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", address, response.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("download exceeds %d bytes", limit)
	}
	return raw, nil
}

// AssetName is the release archive for this platform.
func AssetName() string { return fmt.Sprintf("agentgate_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH) }

// Latest returns the tag of the newest release.
func Latest() (string, error) {
	raw, err := getLimited(apiBase+"/repos/"+Repo+"/releases/latest", 1<<20)
	if err != nil {
		return "", err
	}
	var release struct {
		Tag string `json:"tag_name"`
	}
	if err := json.Unmarshal(raw, &release); err != nil || !safeTag.MatchString(release.Tag) {
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
	if !safeTag.MatchString(tag) {
		return fmt.Errorf("invalid release tag %q", tag)
	}
	base := downloadBase + "/" + Repo + "/releases/download/" + tag + "/"
	sums, err := getLimited(base+"checksums.txt", 1<<20)
	if err != nil {
		return err
	}
	want := ""
	for _, line := range strings.Split(string(sums), "\n") {
		if fields := strings.Fields(line); len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == AssetName() {
			if want != "" {
				return errors.New("release has duplicate checksums for this platform")
			}
			want = fields[0]
		}
	}
	if want == "" {
		return fmt.Errorf("release %s has no build for %s/%s", tag, runtime.GOOS, runtime.GOARCH)
	}
	if decoded, err := hex.DecodeString(want); err != nil || len(decoded) != sha256.Size {
		return errors.New("release checksum is not a SHA-256 digest")
	}
	archive, err := get(base + AssetName())
	if err != nil {
		return err
	}
	if sum := sha256.Sum256(archive); !strings.EqualFold(hex.EncodeToString(sum[:]), want) {
		return errors.New("checksum mismatch; nothing was installed")
	}
	binary, err := extract(archive)
	if err != nil {
		return err
	}
	staged, err := os.CreateTemp(filepath.Dir(target), ".agentgate-update-*")
	if err != nil {
		return fmt.Errorf("cannot write next to %s (%w); reinstall with the install script or use sudo", target, err)
	}
	defer func() { staged.Close(); os.Remove(staged.Name()) }()
	if err := staged.Chmod(0o755); err != nil {
		return err
	}
	if _, err := staged.Write(binary); err != nil {
		return err
	}
	if err := staged.Sync(); err != nil {
		return err
	}
	if err := staged.Close(); err != nil {
		return err
	}
	if err := os.Rename(staged.Name(), target); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(target))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func extract(archive []byte) ([]byte, error) {
	unzipped, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	defer unzipped.Close()
	// Also bound decompressed data, including skipped archive entries.
	reader := tar.NewReader(io.LimitReader(unzipped, maxDownloadSize+(1<<20)))
	for {
		header, err := reader.Next()
		if err != nil {
			return nil, errors.New("the archive has no agentgate binary")
		}
		if (header.Name == "agentgate" || header.Name == "./agentgate") && header.Typeflag == tar.TypeReg {
			if header.Size <= 0 || header.Size > maxDownloadSize {
				return nil, errors.New("release binary is empty or exceeds the size limit")
			}
			raw, err := io.ReadAll(reader)
			if err != nil {
				return nil, fmt.Errorf("read release binary: %w", err)
			}
			if int64(len(raw)) != header.Size {
				return nil, errors.New("truncated release binary")
			}
			return raw, nil
		}
	}
}
