package dev

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/knaus94/agentgate/internal/config"
)

// resourceCapacity is how many workers fit in the free CPU and memory, honouring cgroup limits.
func resourceCapacity(cfg *config.Config) int {
	cpus := runtime.NumCPU()
	memory := int64(1) << 62
	if raw, err := os.ReadFile("/proc/meminfo"); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			if fields := strings.Fields(line); len(fields) >= 2 && fields[0] == "MemAvailable:" {
				kib, _ := strconv.ParseInt(fields[1], 10, 64)
				memory = kib * 1024
			}
		}
	}
	group := "/"
	if raw, err := os.ReadFile("/proc/self/cgroup"); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			if rest, ok := strings.CutPrefix(line, "0::"); ok {
				group = rest
			}
		}
	}
	for dir := filepath.Join("/sys/fs/cgroup", group); strings.HasPrefix(dir, "/sys/fs/cgroup"); dir = filepath.Dir(dir) {
		limit, _ := os.ReadFile(filepath.Join(dir, "memory.max"))
		used, _ := os.ReadFile(filepath.Join(dir, "memory.current"))
		if value, err := strconv.ParseInt(strings.TrimSpace(string(limit)), 10, 64); err == nil {
			current, _ := strconv.ParseInt(strings.TrimSpace(string(used)), 10, 64)
			memory = min(memory, max(0, value-current))
		}
		if quota := strings.Fields(string(readOr(filepath.Join(dir, "cpu.max")))); len(quota) == 2 && quota[0] != "max" {
			q, _ := strconv.Atoi(quota[0])
			p, _ := strconv.Atoi(quota[1])
			if p > 0 {
				cpus = min(cpus, max(1, q/p))
			}
		}
	}
	return int(min(int64(cpus*2), memory/(int64(cfg.WorkerMemoryMiB)<<20)))
}

func readOr(path string) []byte { raw, _ := os.ReadFile(path); return raw }

// lowSpace warns when the disk of a directory has less free space than the configuration asks for: a full disk
// breaks installs and generators without saying so, and their failures look like defects of the code. "" when
// there is enough, or the configuration asks for no warning.
func lowSpace(cfg *config.Config, dir string) string {
	var stat syscall.Statfs_t
	if cfg.MinFreeGiB <= 0 || syscall.Statfs(dir, &stat) != nil {
		return ""
	}
	free := float64(stat.Bavail) * float64(stat.Bsize) / (1 << 30)
	if free >= float64(cfg.MinFreeGiB) {
		return ""
	}
	return fmt.Sprintf("only %.1f GiB free on the disk of %s (min_free_gib %d): installs and generators fail without saying so when it fills; "+
		"tell the user before the work goes on", free, dir, cfg.MinFreeGiB)
}
