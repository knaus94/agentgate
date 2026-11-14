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

func TestUpdateNeverFollowsPredictableStagingSymlinks(t *testing.T) {
	payload := archive(t, "replacement")
	sum := sha256.Sum256(payload)
	serve(t, payload, hex.EncodeToString(sum[:]))
	dir := t.TempDir()
	target, victim := filepath.Join(dir, "agentgate"), filepath.Join(dir, "unrelated")
	if err := os.WriteFile(victim, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, target+".new"); err != nil {
		t.Fatal(err)
	}
	if err := ApplyTo("v9.9.9", target); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(victim); string(raw) != "keep me" {
		t.Fatal("predictable staging path overwrote an unrelated file")
	}
	staging, _ := filepath.Glob(filepath.Join(dir, ".agentgate-update-*"))
	if len(staging) != 0 {
		t.Fatalf("staging files left after replacement: %v", staging)
	}
}

func TestBoundedDownloadRejectsOversizeInsteadOfTruncating(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(strings.Repeat("x", 17))) }))
	defer server.Close()
	if _, err := getLimited(server.URL, 16); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized response accepted as truncated content: %v", err)
	}
	if _, err := getLimited("://bad", 16); err == nil {
		t.Fatal("malformed request should report an error")
	}
}

func TestInvalidReleaseTagIsRejectedBeforeNetwork(t *testing.T) {
	for _, tag := range []string{"", "../main", "v1/new", "v1?token=oops", "v1\nheader"} {
		if err := ApplyTo(tag, filepath.Join(t.TempDir(), "agentgate")); err == nil || !strings.Contains(err.Error(), "invalid release tag") {
			t.Fatalf("tag %q was accepted: %v", tag, err)
		}
	}
}

func TestExtractRejectsEmptyOversizedAndNestedBinaries(t *testing.T) {
	for _, header := range []*tar.Header{
		{Name: "agentgate", Typeflag: tar.TypeReg, Size: 0},
		{Name: "agentgate", Typeflag: tar.TypeReg, Size: maxDownloadSize + 1},
		{Name: "nested/agentgate", Typeflag: tar.TypeReg, Size: 0},
		{Name: "agentgate", Typeflag: tar.TypeSymlink, Linkname: "/bin/sh"},
	} {
		var buffer bytes.Buffer
		gz := gzip.NewWriter(&buffer)
		tw := tar.NewWriter(gz)
		if err := tw.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		_ = tw.Close() // the oversized header is intentionally truncated
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := extract(buffer.Bytes()); err == nil {
			t.Errorf("unsafe archive entry accepted: %+v", header)
		}
	}
}
