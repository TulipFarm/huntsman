package main

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Benchmarks hit the CPU limit at 16–22 pages per core, and a worker peaked near 70 MB.
const (
	workersPerCPU   = 16
	memoryPerWorker = 96 << 20
	maxWorkers      = 1024
)

// defaultWorkers sizes the browser pool to the CPU and memory available, including container limits.
func defaultWorkers() int {
	return workersFor(runtime.GOMAXPROCS(0), memoryLimit("/"))
}

func workersFor(cpus int, memory int64) int {
	n := cpus * workersPerCPU
	if memory > 0 {
		n = min(n, int(memory/memoryPerWorker))
	}
	return max(1, min(n, maxWorkers))
}

// memoryLimit returns the smaller of the cgroup limit and physical memory in bytes, or 0 if neither is known.
func memoryLimit(root string) int64 {
	var limit int64
	for _, path := range []string{"sys/fs/cgroup/memory.max", "sys/fs/cgroup/memory/memory.limit_in_bytes"} {
		data, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			continue
		}
		// cgroup v1 reports "no limit" as a number near 2^63.
		if n, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64); err == nil && n > 0 && n < 1<<60 {
			limit = n
			break
		}
	}
	if total := memTotal(filepath.Join(root, "proc/meminfo")); total > 0 && (limit == 0 || total < limit) {
		limit = total
	}
	return limit
}

func memTotal(path string) int64 {
	file, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 && fields[0] == "MemTotal:" {
			kb, _ := strconv.ParseInt(fields[1], 10, 64)
			return kb << 10
		}
	}
	return 0
}
