package update

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestShellInstallerVerifiesAndAtomicallyInstallsOffline(t *testing.T) {
	payload := archive(t, "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$AGENTGATE_TEST_LOG\"\nexit 0\n")
	sum := sha256.Sum256(payload)
	for _, valid := range []bool{true, false} {
		name := "valid"
		if !valid {
			name = "corrupt-checksum"
		}
		t.Run(name, func(t *testing.T) {
			checksum := hex.EncodeToString(sum[:])
			if !valid {
				checksum = strings.Repeat("0", 64)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "checksums.txt") {
					_, _ = w.Write([]byte(checksum + "  " + AssetName() + "\n"))
				} else if strings.HasSuffix(r.URL.Path, AssetName()) {
					_, _ = w.Write(payload)
				} else {
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			root := t.TempDir()
			bin := filepath.Join(root, "bin with spaces")
			if err := os.MkdirAll(bin, 0o700); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(bin, "agentgate")
			if err := os.WriteFile(target, []byte("original"), 0o755); err != nil {
				t.Fatal(err)
			}
			log := filepath.Join(root, "calls.txt")
			cmd := exec.Command("sh", "../../scripts/install.sh")
			cmd.Env = append(os.Environ(), "HOME="+root, "AGENTGATE_BIN="+bin, "AGENTGATE_VERSION=v9.9.9", "AGENTGATE_LEAD=codex", "AGENTGATE_BASE_URL="+server.URL, "AGENTGATE_TEST_LOG="+log)
			output, err := cmd.CombinedOutput()
			if valid {
				if err != nil {
					t.Fatalf("install failed: %v\n%s", err, output)
				}
				raw, err := os.ReadFile(log)
				if err != nil || string(raw) != "install --lead codex\ndoctor\n" {
					t.Fatalf("installed CLI was not initialized: %q %v", raw, err)
				}
			} else {
				if err == nil {
					t.Fatal("checksum mismatch installed a binary")
				}
				if raw, _ := os.ReadFile(target); string(raw) != "original" {
					t.Fatal("failed installation damaged existing binary")
				}
			}
			staging, _ := filepath.Glob(filepath.Join(bin, ".agentgate.*"))
			if len(staging) != 0 {
				t.Fatal("staging files were left after installer completion")
			}
		})
	}
}
