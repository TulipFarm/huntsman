package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkersFor(t *testing.T) {
	const gb = 1 << 30
	for _, tc := range []struct {
		cpus   int
		memory int64
		want   int
	}{
		{2, 4 * gb, 32},
		{8, 16 * gb, 128},
		{8, 2 * gb, 21},
		{4, 0, 64},
		{1, 64 << 20, 1},
		{128, 1024 * gb, 1024},
	} {
		if got := workersFor(tc.cpus, tc.memory); got != tc.want {
			t.Errorf("workersFor(%d, %d) = %d, want %d", tc.cpus, tc.memory, got, tc.want)
		}
	}
}

func TestMemoryLimit(t *testing.T) {
	write := func(root, path, data string) {
		t.Helper()
		path = filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	meminfo := "MemTotal:       16384000 kB\nMemFree:        1000 kB\n"
	for name, tc := range map[string]struct {
		files map[string]string
		want  int64
	}{
		"cgroup v2 limit":        {map[string]string{"sys/fs/cgroup/memory.max": "2147483648\n", "proc/meminfo": meminfo}, 2 << 30},
		"cgroup v2 unlimited":    {map[string]string{"sys/fs/cgroup/memory.max": "max\n", "proc/meminfo": meminfo}, 16384000 << 10},
		"cgroup v1 unlimited":    {map[string]string{"sys/fs/cgroup/memory/memory.limit_in_bytes": "9223372036854771712\n", "proc/meminfo": meminfo}, 16384000 << 10},
		"limit above host total": {map[string]string{"sys/fs/cgroup/memory.max": "68719476736\n", "proc/meminfo": meminfo}, 16384000 << 10},
		"unknown":                {nil, 0},
	} {
		root := t.TempDir()
		for path, data := range tc.files {
			write(root, path, data)
		}
		if got := memoryLimit(root); got != tc.want {
			t.Errorf("%s: memoryLimit = %d, want %d", name, got, tc.want)
		}
	}
}
