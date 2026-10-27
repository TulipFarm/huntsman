package main

import (
	"encoding/json"
	"flag"
	"math"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
)

func cmdHost(args []string) {
	fs := flag.NewFlagSet("host", flag.ExitOnError)
	size := fs.String("size", "local", "machine name recorded in the output")
	fs.Usage = usageFor(fs, "bench host [--size small]", "Prints this machine's CPU, memory, disk and Docker version as JSON.")
	fs.Parse(args)
	cpu := ""
	if out, err := exec.Command("lscpu").Output(); err == nil {
		for _, l := range strings.Split(string(out), "\n") {
			if k, v, ok := strings.Cut(l, ":"); ok && strings.TrimSpace(k) == "Model name" {
				cpu = strings.TrimSpace(v)
				break
			}
		}
	}
	instance := "unknown"
	if out, err := exec.Command("cloud-init", "query", "ds.meta_data.instance-type").Output(); err == nil && len(out) > 0 {
		instance = strings.TrimSpace(string(out))
	}
	var st syscall.Statfs_t
	diskGB := 0
	if syscall.Statfs("/", &st) == nil {
		diskGB = int(math.Ceil(float64(uint64(st.Blocks)*uint64(st.Bsize)) / (1 << 30)))
	}
	dockerVersion, _ := docker("version", "--format", "{{.Server.Version}}")
	b, _ := json.MarshalIndent(struct {
		Size         string `json:"size"`
		VCPUs        int    `json:"vcpus"`
		MemBytes     int64  `json:"mem_bytes"`
		CPU          string `json:"cpu"`
		InstanceType string `json:"instance_type"`
		DiskGB       int    `json:"disk_gb"`
		Docker       string `json:"docker"`
	}{*size, runtime.NumCPU(), hostMemoryBytes(), cpu, instance, diskGB, dockerVersion}, "", "  ")
	os.Stdout.Write(append(b, '\n'))
}
