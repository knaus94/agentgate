package provider

import (
	"os"
	"path/filepath"
)

// Sandbox wraps argv so that the host is read-only and only the listed directories are writable.
// A private /dev/shm lets a headless browser run; /run/systemd/resolve stays visible for DNS.
// /tmp and /run are replaced by empty private directories, so anything a process needs from there
// is bound back explicitly: readable directories read-only, the lane and the writable ones read-write.
func Sandbox(dir string, writer bool, readable, writable []string, argv []string) []string {
	// A mount cannot be made through a symbolic link, and a path recorded before a directory moved may hold
	// one (the state of an installation under the tool's earlier name is a link to its new place). Mounting
	// the real path serves both: inside, the link still leads to it.
	real := func(path string) string {
		if absolute, err := filepath.Abs(path); err == nil {
			path = absolute
		}
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			return resolved
		}
		return path
	}
	dir = real(dir)
	// Calls may share these slices across parallel lanes. Normalization is local.
	readable = append([]string(nil), readable...)
	writable = append([]string(nil), writable...)
	for index := range readable {
		readable[index] = real(readable[index])
	}
	for index := range writable {
		writable[index] = real(writable[index])
	}
	cwd, _ := os.Getwd()
	cwd = real(cwd)
	checkout := "--ro-bind"
	if writer {
		checkout = "--bind"
	}
	out := []string{"bwrap", "--ro-bind", "/", "/", "--proc", "/proc", "--dev", "/dev", "--tmpfs", "/dev/shm",
		"--unshare-pid", "--unshare-ipc", "--unshare-uts", "--new-session", "--die-with-parent",
		"--tmpfs", "/tmp", "--tmpfs", "/run", "--ro-bind-try", "/run/systemd/resolve", "/run/systemd/resolve"}
	for _, extra := range readable {
		out = append(out, "--ro-bind", extra, extra)
	}
	out = append(out, checkout, cwd, cwd, "--bind", dir, dir)
	for _, extra := range writable {
		out = append(out, "--bind", extra, extra)
	}
	// Apply the private mask last: a later bind of HOME, a state directory or
	// any other ancestor would otherwise reveal the lead's private sessions.
	masked := map[string]bool{}
	for _, private := range []string{PrivateDir(), os.Getenv("AGENTGATE_FIGMA_HOME")} {
		if private == "" {
			continue
		}
		private = real(private)
		if info, err := os.Stat(private); err == nil && info.IsDir() && !masked[private] {
			out = append(out, "--tmpfs", private)
			masked[private] = true
		}
	}
	return append(append(out, "--chdir", cwd, "--"), argv...)
}

// PrivateDir holds what belongs to the lead alone, such as a signed-in browser profile. No worker sees inside it.
func PrivateDir() string {
	if base := os.Getenv("XDG_DATA_HOME"); base != "" {
		return filepath.Join(base, "agentgate", "private")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "share", "agentgate", "private")
}
