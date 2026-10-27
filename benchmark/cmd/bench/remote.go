package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const remoteDir = "bench"

// prefixWriter writes each complete line as "[machine] line", optionally copying it to a file.
type prefixWriter struct {
	prefix string
	buf    bytes.Buffer
	copyTo io.Writer
}

func (w *prefixWriter) Write(p []byte) (int, error) {
	w.buf.Write(p)
	for {
		line, err := w.buf.ReadString('\n')
		if err != nil {
			w.buf.Reset()
			w.buf.WriteString(line)
			return len(p), nil
		}
		w.emit(strings.TrimRight(line, "\r\n"))
	}
}

func (w *prefixWriter) emit(line string) {
	if i := strings.LastIndexByte(line, '\r'); i >= 0 {
		line = line[i+1:]
	}
	sayMu.Lock()
	fmt.Printf("%s %s\n", w.prefix, line)
	sayMu.Unlock()
	if w.copyTo != nil {
		io.WriteString(w.copyTo, line+"\n")
	}
}

func (w *prefixWriter) Flush() {
	if w.buf.Len() > 0 {
		w.emit(w.buf.String())
		w.buf.Reset()
	}
}

func msay(m, format string, a ...any) { say("[%s] %s", m, fmt.Sprintf(format, a...)) }

// streamed runs cmd with stdout and stderr shown under the machine's prefix and optionally saved to logPath.
func streamed(m string, cmd *exec.Cmd, logPath string) error {
	w := &prefixWriter{prefix: "[" + m + "]"}
	if logPath != "" {
		os.MkdirAll(filepath.Dir(logPath), 0o755)
		f, err := os.Create(logPath)
		if err == nil {
			defer f.Close()
			w.copyTo = f
		}
	}
	cmd.Stdout, cmd.Stderr = w, w
	err := cmd.Run()
	w.Flush()
	return err
}

type rampFlags struct {
	phases, levels, profileLevels, image              string
	limit, timeoutMS, readyTimeout, baseline, reserve int
	profileSeconds, profileWarmup                     int
	firecrawlScrapeOnly                               bool
}

func (f rampFlags) args() []string {
	var a []string
	if f.phases != "" {
		a = append(a, "--phases", f.phases)
	}
	if f.profileLevels != "" {
		a = append(a, "--profile-levels", f.profileLevels)
	}
	if f.profileSeconds > 0 {
		a = append(a, "--profile-seconds", itoa(f.profileSeconds))
	}
	if f.profileWarmup > 0 {
		a = append(a, "--profile-warmup", itoa(f.profileWarmup))
	}
	if f.levels != "" {
		a = append(a, "--levels", f.levels)
	}
	if f.image != "" {
		a = append(a, "--image", f.image)
	}
	if f.limit > 0 {
		a = append(a, "--limit", itoa(f.limit))
	}
	if f.timeoutMS > 0 {
		a = append(a, "--timeout-ms", itoa(f.timeoutMS))
	}
	if f.readyTimeout > 0 {
		a = append(a, "--ready-timeout", itoa(f.readyTimeout))
	}
	if f.baseline >= 0 {
		a = append(a, "--baseline-level", itoa(f.baseline))
	}
	if f.reserve > 0 {
		a = append(a, "--reserve-mb", itoa(f.reserve))
	}
	if f.firecrawlScrapeOnly {
		a = append(a, "--firecrawl-scrape-only")
	}
	return a
}

func parseEngines(arg string) []string {
	if arg == "all" {
		return engines
	}
	var out []string
	for _, e := range strings.Split(arg, ",") {
		e = strings.TrimSpace(e)
		if _, ok := engineDefaults[e]; !ok {
			fatalf("--engine %q: want one or more of %s, or all", e, strings.Join(engines, ", "))
		}
		out = append(out, e)
	}
	return out
}

// followers holds the live `tail` processes so Ctrl-C can detach cleanly.
var (
	followMu  sync.Mutex
	followers = map[*exec.Cmd]bool{}
	detached  atomic.Bool
)

func onInterrupt(hint string) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ch
		detached.Store(true)
		followMu.Lock()
		for c := range followers {
			if c.Process != nil {
				c.Process.Kill()
			}
		}
		followMu.Unlock()
		say("")
		say("detached. %s", hint)
		os.Exit(130)
	}()
}

func cmdRun(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	machines := fs.String("machine", "", "machines from benchmark/.env: small, medium, large, a comma list, or all")
	engineArg := fs.String("engine", "", "engines: huntsman, firecrawl, crawl4ai, spider, a comma list, or all")
	var rf rampFlags
	fs.StringVar(&rf.phases, "phases", "", "capacity, profile, or both (default both)")
	fs.StringVar(&rf.levels, "levels", "", "capacity levels, e.g. 1,2,4,8 (default depends on the engine)")
	fs.StringVar(&rf.profileLevels, "profile-levels", "", "profile levels (default 1-8, then ~1.5x steps up to the capacity limit plus one)")
	fs.IntVar(&rf.profileSeconds, "profile-seconds", 0, "length of each profile level (default 90)")
	fs.IntVar(&rf.profileWarmup, "profile-warmup", 0, "seconds of each profile level left out of the measurements (default 20)")
	fs.StringVar(&rf.image, "image", "", "override the engine image tag on the VM")
	fs.IntVar(&rf.limit, "limit", 0, "capacity phase: only the first N dataset URLs (default all 1,000)")
	fs.IntVar(&rf.timeoutMS, "timeout-ms", 0, "per-page timeout (default 30000)")
	fs.IntVar(&rf.readyTimeout, "ready-timeout", 0, "seconds to wait for the engine to start (default depends on the engine)")
	fs.IntVar(&rf.baseline, "baseline-level", -1, "first level used as the latency baseline (default depends on the engine)")
	fs.IntVar(&rf.reserve, "reserve-mb", 0, "host memory kept outside the engine container (default 768)")
	fs.BoolVar(&rf.firecrawlScrapeOnly, "firecrawl-scrape-only", false,
		"Firecrawl: start only the API and browser service, without the workers /v1/scrape never uses")
	fs.Usage = usageFor(fs, "bench run --machine <small|medium|large|all> --engine <name|all> [flags]",
		"Ships the dataset and this CLI to each machine, prepares the engine image, runs the benchmark there,\n"+
			"streams its output here, and copies the results to benchmark/results/matrix/<machine>/<engine>/.\n"+
			"Phase 1 (capacity): all dataset URLs at rising concurrency until the engine fails.\n"+
			"Phase 2 (profile): 90 s runs at 1, 2, 3 ... concurrent pages, up to one level past the limit, measuring CPU and memory.\n"+
			"Machines run in parallel; engines run one after another on each machine.\n"+
			"Ctrl-C only detaches: the ramp keeps running on the VM (see `bench attach` and `bench stop`).")
	fs.Parse(args)
	if *machines == "" || *engineArg == "" {
		fs.Usage()
		os.Exit(2)
	}
	cfg := loadConfig()
	ms := cfg.pickMachines(*machines)
	es := parseEngines(*engineArg)
	onInterrupt(fmt.Sprintf("The ramp keeps running on the VM. Watch: bench attach --machine %s · stop: bench stop --machine %s",
		strings.Join(ms, ","), strings.Join(ms, ",")))

	arch := map[string]string{}
	for _, m := range ms {
		out, err := cfg.sshOutput(m, "uname -m")
		if err != nil {
			fatalf("cannot reach %s (%s): %v", m, cfg.Hosts[m], err)
		}
		arch[m] = map[string]string{"aarch64": "arm64", "arm64": "arm64", "x86_64": "amd64"}[out]
		if arch[m] == "" {
			fatalf("%s: unsupported architecture %q", m, out)
		}
		if running := runningEngines(cfg, m); len(running) > 0 {
			fatalf("%s is already running %s: watch it with `bench attach --machine %s`, or `bench stop --machine %s`",
				m, strings.Join(running, ", "), m, m)
		}
	}
	bins := map[string]string{}
	for _, a := range arch {
		if bins[a] == "" {
			bins[a] = buildSelf(a)
		}
	}

	var wg sync.WaitGroup
	failed := atomic.Bool{}
	for _, m := range ms {
		wg.Add(1)
		go func(m string) {
			defer wg.Done()
			if err := runMachine(cfg, m, arch[m], bins[arch[m]], es, rf); err != nil {
				failed.Store(true)
				msay(m, "ERROR: %v", err)
			}
		}(m)
	}
	wg.Wait()
	say("")
	printMatrix(matrixSummary(matrixDir()))
	say("")
	say("refresh the HTML report: go run ./benchmark/cmd/bench report --inject benchmark/results/index.html")
	if failed.Load() {
		os.Exit(1)
	}
}

func buildSelf(arch string) string {
	out := filepath.Join(stateDir(), "bench-linux-"+arch)
	os.MkdirAll(stateDir(), 0o700)
	say("building the bench CLI for linux/%s", arch)
	cmd := exec.Command("go", "build", "-trimpath", "-o", out, "./benchmark/cmd/bench")
	cmd.Dir = repoRoot()
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+arch, "CGO_ENABLED=0")
	if b, err := cmd.CombinedOutput(); err != nil {
		fatalf("go build: %v\n%s", err, b)
	}
	return out
}

func runMachine(cfg Config, m, arch, bin string, es []string, rf rampFlags) error {
	msay(m, "uploading the bench CLI and dataset to %s", cfg.Hosts[m])
	files := map[string]string{
		"bench": bin,
		"dataset/firecrawl-v1/scrape-content-dataset-v1.csv": firecrawlDataset(),
		"benchmark/fixture/Dockerfile.firecrawl":             filepath.Join(benchDir(), "fixture", "Dockerfile.firecrawl"),
		"benchmark/fixture/firecrawl-entrypoint.sh":          filepath.Join(benchDir(), "fixture", "firecrawl-entrypoint.sh"),
	}
	if err := upload(cfg, m, files); err != nil {
		return fmt.Errorf("upload: %w", err)
	}
	local := filepath.Join(matrixDir(), m)
	os.MkdirAll(local, 0o755)
	for _, e := range es {
		msay(m, "== %s: preparing the image", e)
		if err := prepare(cfg, m, arch, e, filepath.Join(local, e+".prepare.log")); err != nil {
			return fmt.Errorf("%s image: %w (full output in %s)", e, err, rel(filepath.Join(local, e+".prepare.log")))
		}
		if id, err := cfg.sshOutput(m, fmt.Sprintf("docker image inspect --format '{{.Id}} {{.RepoDigests}}' %s", imageFor(e, rf))); err == nil {
			os.WriteFile(filepath.Join(local, e+".image"), []byte(id+"\n"), 0o644)
		}
		rampArgs := append([]string{"./bench", "ramp", "--engine", e, "--out", "results/" + m + "/" + e,
			"--dataset", "dataset/firecrawl-v1/scrape-content-dataset-v1.csv"}, rf.args()...)
		script := fmt.Sprintf(`set -e; cd %s; mkdir -p results/%s; rm -rf results/%s/%s results/%s/%s.log
./bench host --size %s > results/%s/host.json
setsid nohup %s > results/%s/%s.log 2>&1 < /dev/null &
echo $! > results/%s/%s.pid`, remoteDir, m, m, e, m, e, m, m, shellJoin(rampArgs), m, e, m, e)
		if out, err := cfg.sshOutput(m, script); err != nil {
			return fmt.Errorf("start %s ramp: %v %s", e, err, out)
		}
		msay(m, "== %s: ramp started on the VM (log: ~/%s/results/%s/%s.log)", e, remoteDir, m, e)
		follow(cfg, m, e)
		if detached.Load() {
			return nil
		}
		if err := fetch(cfg, m, e); err != nil {
			return err
		}
		cfg.sshOutput(m, "docker rm -f "+container+" >/dev/null 2>&1; true")
	}
	return nil
}

func imageFor(engine string, rf rampFlags) string {
	if rf.image != "" {
		return rf.image
	}
	return engineImages[engine]
}

// prepare puts the engine image on the VM: Huntsman and Spider are built here and copied over when the
// VM's copy differs, Crawl4AI is pulled, and Firecrawl is built on the VM from its public images.
func prepare(cfg Config, m, arch, engine, logPath string) error {
	if avail, err := cfg.sshOutput(m, "df -BG --output=avail / | tail -1 | tr -dc 0-9"); err == nil {
		if gbFree, _ := strconv.Atoi(avail); gbFree < 15 {
			msay(m, "only %d GB disk free: removing unused Docker images", gbFree)
			cfg.sshOutput(m, "docker system prune -af >/dev/null")
		}
	}
	switch engine {
	case "huntsman", "spider":
		ref := engineImages[engine]
		if err := buildLocal(m, engine, arch, logPath); err != nil {
			return err
		}
		localID, _ := docker("image", "inspect", "-f", "{{.Id}}", ref)
		remoteID, _ := cfg.sshOutput(m, "docker image inspect -f '{{.Id}}' "+ref+" 2>/dev/null || true")
		if localID != "" && localID == remoteID {
			msay(m, "%s is already on the VM (%s)", ref, short(localID))
			return nil
		}
		return shipImage(cfg, m, ref)
	case "crawl4ai":
		return streamed(m, cfg.ssh(m, "docker pull "+engineImages[engine]), logPath)
	case "firecrawl":
		return streamed(m, cfg.ssh(m, "cd "+remoteDir+" && docker build --progress=plain -f benchmark/fixture/Dockerfile.firecrawl"+
			" --build-arg API_IMAGE=ghcr.io/firecrawl/firecrawl:latest"+
			" --build-arg BROWSER_IMAGE=ghcr.io/firecrawl/playwright-service:latest"+
			" --build-arg POSTGRES_IMAGE=ghcr.io/firecrawl/nuq-postgres:latest"+
			" -t firecrawl-bench:latest ."), logPath)
	}
	return fmt.Errorf("unknown engine %s", engine)
}

var (
	buildMu   sync.Mutex
	buildDone = map[string]error{}
)

func buildLocal(m, engine, arch, logPath string) error {
	buildMu.Lock()
	defer buildMu.Unlock()
	key := engine + "/" + arch
	if err, ok := buildDone[key]; ok {
		return err
	}
	args := []string{"build", "--platform", "linux/" + arch, "-t", engineImages[engine]}
	ctx := repoRoot()
	if engine == "huntsman" {
		args = append(args, "--target", "runtime")
	} else {
		ctx = filepath.Join(benchDir(), "engines", "spider")
	}
	msay(m, "building %s for linux/%s on this machine", engineImages[engine], arch)
	err := streamed(m, exec.Command("docker", append(args, ctx)...), logPath)
	buildDone[key] = err
	return err
}

// shipImage streams `docker save | gzip` into `docker load` on the VM, reporting progress every 10 s.
func shipImage(cfg Config, m, ref string) error {
	msay(m, "copying %s to the VM", ref)
	save := exec.Command("docker", "save", ref)
	saveOut, _ := save.StdoutPipe()
	load := cfg.ssh(m, "gunzip | docker load")
	pr, pw := io.Pipe()
	load.Stdin = pr
	var loadOut bytes.Buffer
	load.Stdout, load.Stderr = &loadOut, &loadOut
	if err := save.Start(); err != nil {
		return err
	}
	if err := load.Start(); err != nil {
		return err
	}
	var sent atomic.Int64
	done := make(chan struct{})
	go func() {
		start := time.Now()
		for {
			select {
			case <-done:
				return
			case <-time.After(10 * time.Second):
				msay(m, "   sent %.0f MB compressed (%s)", float64(sent.Load())/1e6, dur(time.Since(start)))
			}
		}
	}()
	zw, _ := gzip.NewWriterLevel(&countWriter{pw, &sent}, gzip.BestSpeed)
	_, copyErr := io.Copy(zw, saveOut)
	zw.Close()
	pw.Close()
	saveErr := save.Wait()
	loadErr := load.Wait()
	close(done)
	for _, err := range []error{copyErr, saveErr, loadErr} {
		if err != nil {
			return fmt.Errorf("copy %s: %v %s", ref, err, strings.TrimSpace(loadOut.String()))
		}
	}
	msay(m, "%s loaded on the VM (%.0f MB sent)", ref, float64(sent.Load())/1e6)
	return nil
}

type countWriter struct {
	w io.Writer
	n *atomic.Int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n.Add(int64(n))
	return n, err
}

func upload(cfg Config, m string, files map[string]string) error {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for dst, src := range files {
		b, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		mode := int64(0o644)
		if st, _ := os.Stat(src); st != nil && st.Mode()&0o111 != 0 {
			mode = 0o755
		}
		tw.WriteHeader(&tar.Header{Name: dst, Mode: mode, Size: int64(len(b)), ModTime: time.Now()})
		tw.Write(b)
	}
	tw.Close()
	gz.Close()
	cmd := cfg.ssh(m, "mkdir -p "+remoteDir+" && tar xzf - -C "+remoteDir)
	cmd.Stdin = &buf
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%v %s", err, out)
	}
	return nil
}

// follow streams the remote ramp log until the ramp process exits.
func follow(cfg Config, m, e string) {
	pidFile := fmt.Sprintf("%s/results/%s/%s.pid", remoteDir, m, e)
	logFile := fmt.Sprintf("%s/results/%s/%s.log", remoteDir, m, e)
	cmd := cfg.ssh(m, fmt.Sprintf("tail -n +1 -F --pid=$(cat %s) %s 2>/dev/null", pidFile, logFile))
	followMu.Lock()
	followers[cmd] = true
	followMu.Unlock()
	streamed(m, cmd, "")
	followMu.Lock()
	delete(followers, cmd)
	followMu.Unlock()
}

// fetch copies a finished (or partial) ramp from the VM into benchmark/results/matrix/<machine>/.
func fetch(cfg Config, m, e string) error {
	os.MkdirAll(matrixDir(), 0o755)
	tmp, err := os.MkdirTemp(matrixDir(), ".fetch-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	ssh := cfg.ssh(m, fmt.Sprintf("cd %s/results && tar czf - %s/%s %s/%s.log %s/host.json 2>/dev/null; true", remoteDir, m, e, m, e, m))
	untar := exec.Command("tar", "xzf", "-", "-C", tmp)
	untar.Stdin, _ = ssh.StdoutPipe()
	if err := untar.Start(); err != nil {
		return err
	}
	if err := ssh.Run(); err != nil {
		return fmt.Errorf("fetch %s: %v", e, err)
	}
	if err := untar.Wait(); err != nil {
		return fmt.Errorf("fetch %s: unpack: %v", e, err)
	}
	local := filepath.Join(matrixDir(), m)
	os.MkdirAll(local, 0o755)
	// An unfinished ramp goes to <engine>.partial so it never replaces a complete earlier result.
	complete := fileExists(filepath.Join(tmp, m, e, "ramp.json"))
	dst, logName := filepath.Join(local, e), e+".log"
	if !complete {
		dst, logName = dst+".partial", e+".partial.log"
	}
	os.RemoveAll(dst)
	if fileExists(filepath.Join(tmp, m, e)) {
		if err := os.Rename(filepath.Join(tmp, m, e), dst); err != nil {
			return err
		}
	}
	if b, err := os.ReadFile(filepath.Join(tmp, m, e+".log")); err == nil {
		os.WriteFile(filepath.Join(local, logName), b, 0o644)
	}
	if !complete {
		msay(m, "%s has no ramp.json (stopped or still running): saved what exists to %s", e, rel(dst))
		return nil
	}
	if b, err := os.ReadFile(filepath.Join(tmp, m, "host.json")); err == nil {
		os.WriteFile(filepath.Join(local, "host.json"), b, 0o644)
	}
	os.RemoveAll(filepath.Join(local, e+".partial"))
	os.Remove(filepath.Join(local, e+".partial.log"))
	msay(m, "== %s: results saved to %s", e, rel(filepath.Join(dst, "ramp.json")))
	return nil
}

// runningEngines lists engines whose ramp process is alive on the machine.
func runningEngines(cfg Config, m string) []string {
	out, _ := cfg.sshOutput(m, fmt.Sprintf(`cd %s/results/%s 2>/dev/null || exit 0
for f in *.pid; do [ -f "$f" ] && kill -0 "$(cat "$f")" 2>/dev/null && echo "${f%%.pid}"; done; true`, remoteDir, m))
	if out == "" {
		return nil
	}
	return strings.Fields(out)
}

func cmdAttach(args []string) {
	fs := flag.NewFlagSet("attach", flag.ExitOnError)
	machines := fs.String("machine", "all", "machines to watch")
	fs.Usage = usageFor(fs, "bench attach [--machine small]",
		"Streams the output of ramps already running on the machines, then copies their results here.")
	fs.Parse(args)
	cfg := loadConfig()
	ms := cfg.pickMachines(*machines)
	onInterrupt("The ramp keeps running on the VM.")
	var wg sync.WaitGroup
	for _, m := range ms {
		running := runningEngines(cfg, m)
		if len(running) == 0 {
			msay(m, "nothing running")
			continue
		}
		for _, e := range running {
			wg.Add(1)
			go func(m, e string) {
				defer wg.Done()
				msay(m, "== attached to %s", e)
				follow(cfg, m, e)
				if !detached.Load() {
					if err := fetch(cfg, m, e); err != nil {
						msay(m, "ERROR: %v", err)
					}
				}
			}(m, e)
		}
	}
	wg.Wait()
}

func cmdStop(args []string) {
	fs := flag.NewFlagSet("stop", flag.ExitOnError)
	machines := fs.String("machine", "", "machines whose ramp to stop (or all)")
	fs.Usage = usageFor(fs, "bench stop --machine <small|all>", "Stops the running ramp and engine container on the machines.")
	fs.Parse(args)
	if *machines == "" {
		fs.Usage()
		os.Exit(2)
	}
	cfg := loadConfig()
	for _, m := range cfg.pickMachines(*machines) {
		running := runningEngines(cfg, m)
		for _, e := range running {
			cfg.sshOutput(m, fmt.Sprintf("kill -- -$(cat %s/results/%s/%s.pid) 2>/dev/null; kill $(cat %s/results/%s/%s.pid) 2>/dev/null; true",
				remoteDir, m, e, remoteDir, m, e))
		}
		cfg.sshOutput(m, "docker rm -f "+container+" >/dev/null 2>&1; true")
		if len(running) == 0 {
			msay(m, "nothing was running; removed any leftover engine container")
		} else {
			msay(m, "stopped %s", strings.Join(running, ", "))
		}
	}
}

func cmdStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	fs.Usage = usageFor(fs, "bench status", "Shows each machine in benchmark/.env: reachable, running ramps, last log line, finished results.")
	fs.Parse(args)
	cfg := loadConfig()
	for _, m := range cfg.pickMachines("all") {
		info, err := cfg.sshOutput(m, fmt.Sprintf(`echo "$(nproc) vCPU, $(awk '/MemTotal/ {printf "%%.1f GB", $2/1048576}' /proc/meminfo), $(df -BG --output=avail / | tail -1 | tr -d ' ') disk free"
cd %s/results/%s 2>/dev/null || exit 0
for f in *.pid; do [ -f "$f" ] || continue; e=${f%%.pid}
  if kill -0 "$(cat "$f")" 2>/dev/null; then echo "running $e: $(tail -n 1 $e.log)"; fi; done
for d in */; do [ -f "$d/ramp.json" ] && echo "finished ${d%%/}"; done; true`, remoteDir, m))
		if err != nil {
			msay(m, "%s unreachable: %v", cfg.Hosts[m], err)
			continue
		}
		lines := strings.Split(info, "\n")
		msay(m, "%s · %s", cfg.Hosts[m], lines[0])
		for _, l := range lines[1:] {
			msay(m, "   %s", l)
		}
	}
}

func cmdFetch(args []string) {
	fs := flag.NewFlagSet("fetch", flag.ExitOnError)
	machines := fs.String("machine", "all", "machines")
	engineArg := fs.String("engine", "all", "engines")
	fs.Usage = usageFor(fs, "bench fetch [--machine small] [--engine firecrawl]", "Copies results from the machines without running anything.")
	fs.Parse(args)
	cfg := loadConfig()
	for _, m := range cfg.pickMachines(*machines) {
		for _, e := range parseEngines(*engineArg) {
			if out, _ := cfg.sshOutput(m, fmt.Sprintf("test -d %s/results/%s/%s && echo yes", remoteDir, m, e)); out != "yes" {
				continue
			}
			if err := fetch(cfg, m, e); err != nil {
				msay(m, "ERROR: %v", err)
			}
		}
	}
}

func cmdSSH(args []string) {
	fs := flag.NewFlagSet("ssh", flag.ExitOnError)
	machine := fs.String("machine", "", "machine to open a shell on")
	fs.Usage = usageFor(fs, "bench ssh --machine small [command]", "Opens a shell (or runs a command) on a machine.")
	fs.Parse(args)
	cfg := loadConfig()
	ms := cfg.pickMachines(*machine)
	if len(ms) != 1 {
		fatalf("--machine must name one machine")
	}
	cmd := exec.Command("ssh", append([]string{"-t"}, cfg.sshArgs(ms[0], fs.Args()...)...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			os.Exit(ee.ExitCode())
		}
		fatalf("%v", err)
	}
}

func shellJoin(args []string) string {
	q := make([]string, len(args))
	for i, a := range args {
		if a != "" && strings.Trim(a, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./,=:") == "" {
			q[i] = a
		} else {
			q[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
	}
	return strings.Join(q, " ")
}

func short(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
