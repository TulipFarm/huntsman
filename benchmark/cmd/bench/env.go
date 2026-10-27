package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// repoRoot walks up from the working directory to the directory that contains benchmark/cmd/bench.
func repoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		fatalf("%v", err)
	}
	for d := dir; ; d = filepath.Dir(d) {
		if st, err := os.Stat(filepath.Join(d, "benchmark", "cmd", "bench")); err == nil && st.IsDir() {
			return d
		}
		if filepath.Dir(d) == d {
			fatalf("run bench from inside the repository (no benchmark/cmd/bench above %s)", dir)
		}
	}
}

func benchDir() string   { return filepath.Join(repoRoot(), "benchmark") }
func envPath() string    { return filepath.Join(benchDir(), ".env") }
func stateDir() string   { return filepath.Join(benchDir(), "aws", ".state") }
func matrixDir() string  { return filepath.Join(benchDir(), "results", "matrix") }
func defaultKey() string { return filepath.Join(stateDir(), "id_ed25519") }
func firecrawlDataset() string {
	return filepath.Join(repoRoot(), "dataset", "firecrawl-v1", "scrape-content-dataset-v1.csv")
}

// Config is benchmark/.env: how to reach each machine.
type Config struct {
	User  string
	Key   string
	Hosts map[string]string // machine name -> host or IP
}

func loadConfig() Config {
	f, err := os.Open(envPath())
	if err != nil {
		fatalf("no %s: run `bench provision`, or write it by hand (see benchmark/.env.example)", rel(envPath()))
	}
	defer f.Close()
	c := Config{User: "ubuntu", Key: defaultKey(), Hosts: map[string]string{}}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.Trim(strings.TrimSpace(v), `"'`)
		switch {
		case k == "BENCH_SSH_USER":
			c.User = v
		case k == "BENCH_SSH_KEY":
			if strings.HasPrefix(v, "~/") {
				home, _ := os.UserHomeDir()
				v = filepath.Join(home, v[2:])
			} else if !filepath.IsAbs(v) {
				v = filepath.Join(repoRoot(), v)
			}
			c.Key = v
		case strings.HasPrefix(k, "BENCH_HOST_") && v != "":
			c.Hosts[strings.ToLower(strings.TrimPrefix(k, "BENCH_HOST_"))] = v
		}
	}
	if len(c.Hosts) == 0 {
		fatalf("%s lists no BENCH_HOST_<MACHINE> entries", rel(envPath()))
	}
	return c
}

func writeConfig(hosts map[string]string) {
	var b strings.Builder
	b.WriteString("# Written by `bench provision`; removed by `bench destroy`.\n")
	b.WriteString("# Any SSH-reachable Linux machine with Docker works: add BENCH_HOST_<NAME>=<host or IP>.\n")
	b.WriteString("BENCH_SSH_USER=ubuntu\n")
	fmt.Fprintf(&b, "BENCH_SSH_KEY=%s\n", rel(defaultKey()))
	for _, name := range sortedMachines(hosts) {
		fmt.Fprintf(&b, "BENCH_HOST_%s=%s\n", strings.ToUpper(name), hosts[name])
	}
	if err := os.WriteFile(envPath(), []byte(b.String()), 0o600); err != nil {
		fatalf("%v", err)
	}
}

var machineOrder = map[string]int{"small": 0, "medium": 1, "large": 2}

func sortedMachines(hosts map[string]string) []string {
	names := make([]string, 0, len(hosts))
	for n := range hosts {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		oi, iok := machineOrder[names[i]]
		oj, jok := machineOrder[names[j]]
		if iok != jok {
			return iok
		}
		if iok && oi != oj {
			return oi < oj
		}
		return names[i] < names[j]
	})
	return names
}

// pickMachines resolves "small,large" or "all" against the configured hosts.
func (c Config) pickMachines(arg string) []string {
	if arg == "" || arg == "all" {
		return sortedMachines(c.Hosts)
	}
	var out []string
	for _, m := range strings.Split(arg, ",") {
		m = strings.TrimSpace(m)
		if _, ok := c.Hosts[m]; !ok {
			fatalf("machine %q is not in %s (have: %s)", m, rel(envPath()), strings.Join(sortedMachines(c.Hosts), ", "))
		}
		out = append(out, m)
	}
	return out
}

func (c Config) sshArgs(machine string, extra ...string) []string {
	args := []string{"-i", c.Key, "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR", "-o", "ServerAliveInterval=30", "-o", "ConnectTimeout=10", "-o", "BatchMode=yes"}
	host := c.Hosts[machine]
	if h, port, ok := strings.Cut(host, ":"); ok {
		host = h
		args = append(args, "-p", port)
	}
	args = append(args, c.User+"@"+host)
	return append(args, extra...)
}

func (c Config) ssh(machine, script string) *exec.Cmd {
	return exec.Command("ssh", c.sshArgs(machine, script)...)
}

// sshOutput runs script on the machine and returns its trimmed stdout.
func (c Config) sshOutput(machine, script string) (string, error) {
	cmd := c.ssh(machine, script)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return strings.TrimSpace(string(out)), fmt.Errorf("%s: %v %s", machine, err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}

func rel(p string) string {
	if r, err := filepath.Rel(repoRoot(), p); err == nil && !strings.HasPrefix(r, "..") {
		return r
	}
	return p
}

func fatalf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "bench: "+format+"\n", a...)
	os.Exit(1)
}
