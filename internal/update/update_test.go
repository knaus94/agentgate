package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func archive(t *testing.T, content string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	zipped := gzip.NewWriter(&buffer)
	packed := tar.NewWriter(zipped)
	_ = packed.WriteHeader(&tar.Header{Name: "claude-dev", Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg})
	_, _ = packed.Write([]byte(content))
	_ = packed.Close()
	_ = zipped.Close()
	return buffer.Bytes()
}

func serve(t *testing.T, payload []byte, checksum string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			_, _ = w.Write([]byte(`{"tag_name":"v9.9.9"}`))
		case strings.HasSuffix(r.URL.Path, "/checksums.txt"):
			_, _ = w.Write([]byte(checksum + "  " + AssetName() + "\n" + "0000  other.tar.gz\n"))
		case strings.HasSuffix(r.URL.Path, "/"+AssetName()):
			_, _ = w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	previousAPI, previousDownload := apiBase, downloadBase
	apiBase, downloadBase = server.URL, server.URL
	t.Cleanup(func() { apiBase, downloadBase = previousAPI, previousDownload; server.Close() })
}

func TestUpdateReplacesTheBinaryOnlyWhenTheChecksumMatches(t *testing.T) {
	payload := archive(t, "new binary")
	sum := sha256.Sum256(payload)
	serve(t, payload, hex.EncodeToString(sum[:]))
	tag, err := Latest()
	if err != nil || tag != "v9.9.9" || Current(tag) {
		t.Fatalf("latest: %q %v", tag, err)
	}
	target := filepath.Join(t.TempDir(), "claude-dev")
	if err := os.WriteFile(target, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ApplyTo(tag, target); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(target)
	info, _ := os.Stat(target)
	if string(raw) != "new binary" || info.Mode().Perm() != 0o755 {
		t.Fatalf("the binary must be replaced and executable: %q %v", raw, info.Mode())
	}
	if _, err := os.Stat(target + ".new"); err == nil {
		t.Fatal("no staging file may be left behind")
	}
}

func TestUpdateRefusesACorruptedDownload(t *testing.T) {
	serve(t, archive(t, "tampered"), strings.Repeat("0", 64))
	target := filepath.Join(t.TempDir(), "claude-dev")
	_ = os.WriteFile(target, []byte("old binary"), 0o755)
	if err := ApplyTo("v9.9.9", target); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("want a checksum error, got %v", err)
	}
	if raw, _ := os.ReadFile(target); string(raw) != "old binary" {
		t.Fatal("a failed update must leave the old binary in place")
	}
}

func TestCurrentComparesVersionsWithOrWithoutPrefix(t *testing.T) {
	previous := Version
	Version = "0.1.0"
	defer func() { Version = previous }()
	if !Current("v0.1.0") || !Current("0.1.0") || Current("v0.2.0") {
		t.Fatal("version comparison is wrong")
	}
}
