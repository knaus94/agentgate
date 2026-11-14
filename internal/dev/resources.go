package dev

import (
	"github.com/knaus94/agentgate/internal/config"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
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
