package main

import (
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const stack = "bench"

func pulumiBin() string {
	if p, err := exec.LookPath("pulumi"); err == nil {
		return p
	}
	home, _ := os.UserHomeDir()
	if p := filepath.Join(home, ".pulumi", "bin", "pulumi"); fileExists(p) {
		return p
	}
	fatalf("pulumi not found: install it from https://www.pulumi.com/docs/install/")
	return ""
}

func pulumi(args ...string) *exec.Cmd {
	cmd := exec.Command(pulumiBin(), args...)
	cmd.Dir = filepath.Join(benchDir(), "aws")
	cmd.Env = append(os.Environ(), "PULUMI_BACKEND_URL=file://"+stateDir(), "PULUMI_SKIP_UPDATE_CHECK=true")
	if os.Getenv("PULUMI_CONFIG_PASSPHRASE") == "" && os.Getenv("PULUMI_CONFIG_PASSPHRASE_FILE") == "" {
		cmd.Env = append(cmd.Env, "PULUMI_CONFIG_PASSPHRASE=")
	}
	return cmd
}

func pulumiRun(args ...string) error {
	say("$ pulumi %s", strings.Join(args, " "))
	cmd := pulumi(args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

func cmdProvision(args []string) {
	fs := flag.NewFlagSet("provision", flag.ExitOnError)
	region := fs.String("region", envOr("AWS_REGION", "us-east-1"), "AWS region")
	fs.Usage = usageFor(fs, "bench provision [--region us-east-1]",
		"Creates the small, medium and large VMs with Pulumi (benchmark/aws/Pulumi.yaml), waits until Docker is\n"+
			"installed on each, and writes their addresses to benchmark/.env. Needs AWS credentials in the environment.")
	fs.Parse(args)
	os.MkdirAll(stateDir(), 0o700)
	key := defaultKey()
	if !fileExists(key) {
		say("creating a throwaway SSH key at %s", rel(key))
		if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key).CombinedOutput(); err != nil {
			fatalf("ssh-keygen: %v %s", err, out)
		}
	}
	pub, err := os.ReadFile(key + ".pub")
	if err != nil {
		fatalf("%v", err)
	}
	ip := publicIP()
	say("SSH will be open only to %s", ip)
	steps := [][]string{
		{"stack", "select", "--create", stack},
		{"config", "set", "--stack", stack, "aws:region", *region},
		{"config", "set", "--stack", stack, "sshPublicKey", strings.TrimSpace(string(pub))},
		{"config", "set", "--stack", stack, "sshCidr", ip + "/32"},
		{"up", "--yes", "--skip-preview", "--stack", stack},
	}
	for _, s := range steps {
		if err := pulumiRun(s...); err != nil {
			fatalf("pulumi %s failed: %v (expired AWS credentials? try `aws sso login`)", s[0], err)
		}
	}
	out, err := pulumi("stack", "output", "--json", "--stack", stack).Output()
	if err != nil {
		fatalf("pulumi stack output: %v", err)
	}
	hosts := map[string]string{}
	if err := json.Unmarshal(out, &hosts); err != nil {
		fatalf("pulumi stack output: %v", err)
	}
	writeConfig(hosts)
	say("wrote %s", rel(envPath()))
	cfg := loadConfig()
	var wg sync.WaitGroup
	for _, m := range cfg.pickMachines("all") {
		wg.Add(1)
		go func(m string) {
			defer wg.Done()
			start := time.Now()
			for {
				if _, err := cfg.sshOutput(m, "test -f /var/tmp/ready && docker version --format '{{.Server.Version}}'"); err == nil {
					say("[%s] %s ready after %s", m, cfg.Hosts[m], dur(time.Since(start)))
					return
				}
				if time.Since(start) > 15*time.Minute {
					say("[%s] %s still not ready after 15m; check the instance's console log", m, cfg.Hosts[m])
					return
				}
				time.Sleep(10 * time.Second)
			}
		}(m)
	}
	wg.Wait()
	say("")
	say("next: go run ./benchmark/cmd/bench run --machine all --engine huntsman")
	say("when finished: go run ./benchmark/cmd/bench destroy")
}

func cmdDestroy(args []string) {
	fs := flag.NewFlagSet("destroy", flag.ExitOnError)
	fs.Usage = usageFor(fs, "bench destroy", "Deletes every VM created by `bench provision` and removes benchmark/.env.")
	fs.Parse(args)
	if err := pulumiRun("destroy", "--yes", "--skip-preview", "--stack", stack); err != nil {
		fatalf("pulumi destroy failed: %v\nIf the stack is locked by a process that no longer runs: "+
			"cd benchmark/aws && PULUMI_BACKEND_URL=file://$PWD/.state pulumi cancel --stack %s, then retry. "+
			"If AWS credentials expired, log in again and retry.", err, stack)
	}
	os.Remove(envPath())
	say("VMs deleted; removed %s", rel(envPath()))
}

func publicIP() string {
	c := &http.Client{Timeout: 10 * time.Second}
	resp, err := c.Get("https://checkip.amazonaws.com")
	if err != nil {
		fatalf("look up this machine's public IP: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	ip := strings.TrimSpace(string(b))
	if ip == "" {
		fatalf("empty public IP from checkip.amazonaws.com")
	}
	return ip
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
