package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	container = "bench-engine"
	hostPort  = 18000
)

var engines = []string{"huntsman", "firecrawl", "crawl4ai", "spider"}

// engineDefaults are the ramp flags each engine runs with unless overridden on the command line.
var engineDefaults = map[string]struct {
	levels        string
	readyTimeout  int
	baselineLevel int
}{
	"huntsman": {"8,16,32,64,128,256,512", 300, 0},
	"spider":   {"8,16,32,64,128,256,512", 300, 0},
	"crawl4ai": {"8,16,32,64,128,256,512", 300, 0},
	// Firecrawl does not reach 8 on the smaller machines and takes minutes to start.
	"firecrawl": {"1,2,4,8,16,32,64", 900, 8},
}

var engineImages = map[string]string{
	"huntsman":  "huntsman:bench",
	"firecrawl": "firecrawl-bench:latest",
	"crawl4ai":  "unclecode/crawl4ai:latest",
	"spider":    "spider-bench:latest",
}

type engineSpec struct {
	image string
	port  int
	env   [][2]string
	extra []string
}

var firecrawlScrapeOnly bool

func specFor(engine string, level int, image string) engineSpec {
	if image == "" {
		image = engineImages[engine]
	}
	s := engineSpec{image: image}
	switch engine {
	case "huntsman":
		s.port = 8080
		s.env = [][2]string{{"HUNTSMAN_WORKERS", itoa(level)}, {"HUNTSMAN_PER_HOST", itoa(min(level, 64))}, {"HUNTSMAN_OBEY_ROBOTS", "false"}}
	case "firecrawl":
		s.port = 3002
		s.env = [][2]string{
			// /v1/scrape runs inside the API process; NuQ workers only serve crawl and batch jobs, so one is
			// enough and each extra is an idle Node process. The page pool caps concurrency and keeps Firecrawl's
			// default of 10 as headroom: Playwright can leak a page slot on a hung navigation.
			{"NUQ_WORKER_COUNT", "1"}, {"MAX_CONCURRENT_PAGES", itoa(max(level, 10))},
			// The image pins RabbitMQ to one scheduler; let it use every core.
			{"RABBITMQ_SERVER_ADDITIONAL_ERL_ARGS", ""},
		}
		if firecrawlScrapeOnly {
			s.env = append(s.env, [2]string{"FIRECRAWL_SCRAPE_ONLY", "1"})
		}
	case "crawl4ai":
		s.port = 11235
		s.env = [][2]string{{"CRAWL4AI_API_TOKEN", crawl4aiToken}}
		s.extra = []string{"-v", crawl4aiConfig(image, level) + ":/app/config.yml:ro"}
	case "spider":
		s.port = 8080
		s.env = [][2]string{{"SPIDER_CONCURRENCY", itoa(level)}}
	}
	return s
}

var crawl4aiBase = map[string]string{}

func crawl4aiConfig(image string, level int) string {
	text, ok := crawl4aiBase[image]
	if !ok {
		out, err := exec.Command("docker", "run", "--rm", "--entrypoint", "cat", image, "/app/config.yml").Output()
		if err != nil {
			fatalf("read crawl4ai config.yml: %v", err)
		}
		text = string(out)
		crawl4aiBase[image] = text
	}
	const oldLimit = `default_limit: "1000/minute"`
	if !strings.Contains(text, oldLimit) {
		fatalf("crawl4ai config.yml no longer contains %s", oldLimit)
	}
	text = strings.Replace(text, oldLimit, `default_limit: "1000000/minute"`, 1)
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	inPool, replaced := false, false
	for i, line := range lines {
		if strings.TrimSpace(line) == "pool:" {
			inPool = true
		}
		if inPool && !replaced && strings.HasPrefix(strings.TrimSpace(line), "max_pages:") {
			lines[i] = line[:len(line)-len(strings.TrimLeft(line, " \t"))] + "max_pages: " + itoa(level)
			replaced = true
		}
	}
	if !replaced {
		fatalf("crawl4ai config.yml no longer contains crawler.pool.max_pages")
	}
	path := filepath.Join(os.TempDir(), fmt.Sprintf("crawl4ai-config-%d.yml", level))
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		fatalf("%v", err)
	}
	os.Chmod(path, 0o644)
	return path
}

func docker(args ...string) (string, error) {
	out, err := exec.Command("docker", args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func startEngine(engine string, level int, image string, memory int64) (string, string) {
	docker("rm", "-f", container)
	s := specFor(engine, level, image)
	args := []string{"run", "-d", "--name", container, "-p", fmt.Sprintf("127.0.0.1:%d:%d", hostPort, s.port),
		fmt.Sprintf("--memory=%d", memory), fmt.Sprintf("--memory-swap=%d", memory), "--shm-size=2g"}
	args = append(args, s.extra...)
	for _, kv := range s.env {
		args = append(args, "-e", kv[0]+"="+kv[1])
	}
	out, err := docker(append(args, s.image)...)
	if err != nil {
		fatalf("docker run %s: %v\n%s", s.image, err, out)
	}
	lines := strings.Split(out, "\n")
	return lines[len(lines)-1], fmt.Sprintf("http://127.0.0.1:%d", hostPort)
}

func containerRunning() bool {
	out, _ := docker("inspect", "-f", "{{.State.Running}}", container)
	return out == "true"
}

func waitReady(engine, endpoint string, timeout time.Duration) bool {
	start := time.Now()
	lastNote := start
	for time.Since(start) < timeout {
		if !containerRunning() {
			say("   engine exited during startup after %s", dur(time.Since(start)))
			return false
		}
		r := scrape(endpoint, engine, "https://example.com/", 15000)
		if r.content != "" && r.Error == nil {
			say("   ready after %s", dur(time.Since(start)))
			time.Sleep(5 * time.Second)
			return true
		}
		if time.Since(lastNote) >= 30*time.Second {
			lastNote = time.Now()
			say("   waiting for the engine to answer (%s of %s, memory %s)", dur(time.Since(start)), dur(timeout), gb(containerMemNow()))
		}
		time.Sleep(3 * time.Second)
	}
	say("   engine did not answer within %s", dur(timeout))
	return false
}

func containerMemNow() int64 {
	out, _ := docker("stats", "--no-stream", "--format", "{{.MemUsage}}", container)
	f := strings.Fields(out)
	if len(f) == 0 {
		return 0
	}
	return parseSize(f[0])
}

// Sample is one second of container usage.
type Sample struct {
	T        float64 `json:"t"`
	CPUCores float64 `json:"cpu_cores"`
	MemBytes int64   `json:"mem_bytes"`
}

// Monitor samples container CPU and memory once a second from cgroup v2, falling back to docker stats,
// and records the biggest processes every 10 s so a memory spike can be traced to a process.
type Monitor struct {
	cid      string
	cg       string
	t0       time.Time
	stop     chan struct{}
	done     sync.WaitGroup
	mu       sync.Mutex
	samples  []Sample
	procs    []map[string]any
	lastCPU  atomic.Value
	lastMem  atomic.Int64
	peakSeen atomic.Int64
}

func newMonitor(cid string) *Monitor {
	m := &Monitor{cid: cid, stop: make(chan struct{})}
	for _, p := range []string{"/sys/fs/cgroup/system.slice/docker-" + cid + ".scope", "/sys/fs/cgroup/docker/" + cid} {
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			m.cg = p
			break
		}
	}
	m.lastCPU.Store(0.0)
	return m
}

func (m *Monitor) readCgroup() (int64, int64, error) {
	cpu, err := os.ReadFile(m.cg + "/cpu.stat")
	if err != nil {
		return 0, 0, err
	}
	var usage int64 = -1
	for _, l := range strings.Split(string(cpu), "\n") {
		if f := strings.Fields(l); len(f) == 2 && f[0] == "usage_usec" {
			usage, _ = strconv.ParseInt(f[1], 10, 64)
		}
	}
	cur, err := os.ReadFile(m.cg + "/memory.current")
	if err != nil {
		return 0, 0, err
	}
	mem, _ := strconv.ParseInt(strings.TrimSpace(string(cur)), 10, 64)
	stat := readKV(m.cg + "/memory.stat")
	if usage < 0 {
		return 0, 0, fmt.Errorf("no usage_usec")
	}
	return usage, mem - stat["inactive_file"], nil
}

func (m *Monitor) Start() {
	m.t0 = time.Now()
	m.done.Add(2)
	go m.sampleLoop()
	go m.procLoop()
}

func (m *Monitor) add(s Sample) {
	m.mu.Lock()
	m.samples = append(m.samples, s)
	m.mu.Unlock()
	m.lastCPU.Store(s.CPUCores)
	m.lastMem.Store(s.MemBytes)
	if s.MemBytes > m.peakSeen.Load() {
		m.peakSeen.Store(s.MemBytes)
	}
}

func (m *Monitor) sampleLoop() {
	defer m.done.Done()
	var prevT time.Time
	var prevUsage int64
	for {
		t := time.Now()
		if m.cg != "" {
			if usage, mem, err := m.readCgroup(); err == nil {
				if !prevT.IsZero() {
					cores := float64(usage-prevUsage) / 1e6 / t.Sub(prevT).Seconds()
					m.add(Sample{T: round(t.Sub(m.t0).Seconds(), 2), CPUCores: round(cores, 3), MemBytes: mem})
				}
				prevT, prevUsage = t, usage
			}
		} else if out, err := docker("stats", "--no-stream", "--format", "{{.CPUPerc}} {{.MemUsage}}", m.cid); err == nil {
			if f := strings.Fields(out); len(f) >= 2 {
				pct, _ := strconv.ParseFloat(strings.TrimSuffix(f[0], "%"), 64)
				m.add(Sample{T: round(t.Sub(m.t0).Seconds(), 2), CPUCores: pct / 100, MemBytes: parseSize(f[1])})
			}
		}
		select {
		case <-m.stop:
			return
		case <-time.After(time.Second - time.Since(t)%time.Second):
		}
	}
}

func (m *Monitor) procLoop() {
	defer m.done.Done()
	for {
		if out, err := docker("top", container, "-eo", "pid,rss,args"); err == nil {
			var top []map[string]any
			for _, l := range strings.Split(out, "\n")[1:] {
				f := strings.Fields(l)
				if len(f) < 3 {
					continue
				}
				kb, _ := strconv.ParseInt(f[1], 10, 64)
				cmd := strings.Join(f[2:], " ")
				if len(cmd) > 120 {
					cmd = cmd[:120]
				}
				top = append(top, map[string]any{"rss_bytes": kb * 1024, "cmd": cmd})
			}
			sortByRSS(top)
			if len(top) > 12 {
				top = top[:12]
			}
			m.mu.Lock()
			m.procs = append(m.procs, map[string]any{"t": round(time.Since(m.t0).Seconds(), 1), "top": top})
			m.mu.Unlock()
		}
		select {
		case <-m.stop:
			return
		case <-time.After(10 * time.Second):
		}
	}
}

func (m *Monitor) Finish() map[string]any {
	close(m.stop)
	m.done.Wait()
	res := map[string]any{"mem_bytes_peak_cgroup": nil, "oom_kills": nil}
	if m.cg != "" {
		if b, err := os.ReadFile(m.cg + "/memory.peak"); err == nil {
			if v, _ := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64); v > 0 {
				res["mem_bytes_peak_cgroup"] = v
			}
		}
		if ev := readKV(m.cg + "/memory.events"); ev != nil {
			res["oom_kills"] = ev["oom_kill"]
		}
	}
	for k, v := range m.window(0, math.Inf(1)) {
		res[k] = v
	}
	return res
}

// window averages the samples taken between from and to seconds after Start.
func (m *Monitor) window(from, to float64) map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	var cpuSum, cpuPeak, memSum float64
	var memPeak int64
	var n float64
	for _, s := range m.samples {
		if s.T < from || s.T > to {
			continue
		}
		n++
		cpuSum += s.CPUCores
		cpuPeak = max(cpuPeak, s.CPUCores)
		memSum += float64(s.MemBytes)
		memPeak = max(memPeak, s.MemBytes)
	}
	if n == 0 {
		return map[string]any{"cpu_cores_avg": nil, "cpu_cores_peak": nil, "mem_bytes_avg": nil, "mem_bytes_peak": nil}
	}
	return map[string]any{"cpu_cores_avg": cpuSum / n, "cpu_cores_peak": cpuPeak, "mem_bytes_avg": memSum / n, "mem_bytes_peak": memPeak}
}

func readKV(path string) map[string]int64 {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	out := map[string]int64{}
	for _, l := range strings.Split(string(b), "\n") {
		if f := strings.Fields(l); len(f) == 2 {
			out[f[0]], _ = strconv.ParseInt(f[1], 10, 64)
		}
	}
	return out
}

func parseSize(s string) int64 {
	units := []struct {
		suffix string
		mul    float64
	}{{"KiB", 1 << 10}, {"MiB", 1 << 20}, {"GiB", 1 << 30}, {"kB", 1e3}, {"MB", 1e6}, {"GB", 1e9}, {"B", 1}}
	for _, u := range units {
		if strings.HasSuffix(s, u.suffix) {
			v, _ := strconv.ParseFloat(strings.TrimSuffix(s, u.suffix), 64)
			return int64(v * u.mul)
		}
	}
	v, _ := strconv.ParseFloat(s, 64)
	return int64(v)
}

func hostMemoryBytes() int64 {
	if kv := readMeminfo(); kv["MemTotal:"] > 0 {
		return kv["MemTotal:"] * 1024
	}
	out, _ := docker("info", "--format", "{{.MemTotal}}")
	v, _ := strconv.ParseInt(out, 10, 64)
	return v
}

func readMeminfo() map[string]int64 {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return map[string]int64{}
	}
	defer f.Close()
	out := map[string]int64{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if fl := strings.Fields(sc.Text()); len(fl) >= 2 {
			out[fl[0]], _ = strconv.ParseInt(fl[1], 10, 64)
		}
	}
	return out
}

// runLevel scrapes every task with exactly `concurrency` requests in flight and prints progress every 10 s.
func runLevel(engine, endpoint string, tasks []Task, concurrency, timeoutMS int, out string, mon *Monitor) map[string]any {
	os.MkdirAll(out, 0o755)
	rows := make([]Row, len(tasks))
	var next, finished, answered atomic.Int64
	start := time.Now()
	stopProgress := make(chan struct{})
	go func() {
		for {
			select {
			case <-stopProgress:
				return
			case <-time.After(10 * time.Second):
			}
			n, a := finished.Load(), answered.Load()
			el := time.Since(start)
			say("   %4d/%d done  answered %s  %.2f pages/s  cpu %.2f cores  mem %s (peak %s)  %s",
				n, len(tasks), pct(a, n), float64(n)/el.Seconds(), mon.lastCPU.Load().(float64),
				gb(mon.lastMem.Load()), gb(mon.peakSeen.Load()), dur(el))
		}
	}()
	var wg sync.WaitGroup
	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1)) - 1
				if i >= len(tasks) {
					return
				}
				r := scrape(endpoint, engine, tasks[i].URL, timeoutMS)
				r.ID = tasks[i].ID
				rows[i] = r
				if r.answered() {
					answered.Add(1)
				}
				finished.Add(1)
			}
		}()
	}
	wg.Wait()
	wall := time.Since(start).Seconds()
	close(stopProgress)

	// Score after the timed run so scoring never competes with the engine for CPU.
	var okLat, allLat []float64
	var success, covered int
	var recall, precision, f1 float64
	f, _ := os.Create(filepath.Join(out, "results.jsonl"))
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	for i := range rows {
		rows[i].score(tasks[i])
		r := rows[i]
		enc.Encode(r)
		allLat = append(allLat, r.LatencyMS)
		if r.Success {
			success++
			okLat = append(okLat, r.LatencyMS)
		}
		if r.Recall >= 0.1 {
			covered++
		}
		recall += r.Recall
		precision += r.Precision
		f1 += r.F1
	}
	f.Close()
	n := float64(len(rows))
	summary := map[string]any{
		"engine": engine, "concurrency": concurrency, "urls": len(rows),
		"success_rate": float64(success) / n, "coverage_recall_10pct": float64(covered) / n,
		"avg_recall": recall / n, "avg_precision": precision / n, "avg_f1": f1 / n,
		"latency_ms_p50_all": percentile(allLat, 0.5), "latency_ms_p95_all": percentile(allLat, 0.95),
		"latency_ms_p50_success": percentile(okLat, 0.5), "latency_ms_p95_success": percentile(okLat, 0.95),
		"wall_seconds_this_run": wall, "pages_per_second_this_run": n / wall,
	}
	writeJSON(filepath.Join(out, "summary.json"), summary)
	return summary
}

// profileSteps are the resource-profile levels: every level to 8, then about 1.5x steps.
var profileSteps = []int{1, 2, 3, 4, 5, 6, 7, 8, 12, 16, 24, 32, 48, 64, 96, 128, 192, 256, 384, 512}

// profileLevels returns the profile levels up to the capacity limit plus the next one beyond it.
func profileLevels(limit int) []int {
	var out []int
	for _, l := range profileSteps {
		out = append(out, l)
		if l > limit {
			break
		}
	}
	return out
}

func parseLevels(arg string) []int {
	var lv []int
	for _, s := range strings.Split(arg, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil || n < 1 {
			fatalf("bad level %q", s)
		}
		lv = append(lv, n)
	}
	return lv
}

func joinInts(v []int) string {
	s := make([]string, len(v))
	for i, n := range v {
		s[i] = itoa(n)
	}
	return strings.Join(s, ",")
}

type rampOpts struct {
	engine, image string
	memory        int64
	ready         time.Duration
	timeoutMS     int
}

// inContainer runs work against a fresh engine container configured for level and records whether it crashed.
func inContainer(o rampOpts, level int, dir string, work func(endpoint string, mon *Monitor) map[string]any) (map[string]any, bool) {
	os.MkdirAll(dir, 0o755)
	cid, endpoint := startEngine(o.engine, level, o.image, o.memory)
	rec := map[string]any{"level": level, "container_memory_limit_bytes": o.memory}
	ready := waitReady(o.engine, endpoint, o.ready)
	if ready {
		mon := newMonitor(cid)
		mon.Start()
		sum := work(endpoint, mon)
		for k, v := range mon.Finish() {
			rec[k] = v
		}
		for k, v := range sum {
			rec[k] = v
		}
		writeJSONL(filepath.Join(dir, "resources.jsonl"), mon.samples)
		writeJSONL(filepath.Join(dir, "processes.jsonl"), mon.procs)
	} else {
		rec["passed"], rec["reason"] = false, "engine did not become ready"
	}
	var state struct {
		State struct {
			OOMKilled, Running bool
		}
		RestartCount int
	}
	if raw, err := docker("inspect", "-f", "{{json .}}", container); err == nil {
		json.Unmarshal([]byte(raw), &state)
	}
	rec["oom_killed"], rec["still_running"], rec["restarts"] = state.State.OOMKilled, state.State.Running, state.RestartCount
	logs, _ := exec.Command("docker", "logs", "--tail", "300", container).CombinedOutput()
	os.WriteFile(filepath.Join(dir, "engine.log"), logs, 0o644)
	docker("rm", "-f", container)
	crashed := ready && (state.State.OOMKilled || !state.State.Running || num(rec["oom_kills"]) > 0)
	return rec, crashed
}

func cmdRamp(args []string) {
	fs := flag.NewFlagSet("ramp", flag.ExitOnError)
	engine := fs.String("engine", "", "engine to test: "+strings.Join(engines, ", "))
	out := fs.String("out", "", "output directory (default benchmark/results/matrix/local/<engine>)")
	image := fs.String("image", "", "override the engine image")
	phases := fs.String("phases", "capacity,profile", "capacity, profile, or both")
	levels := fs.String("levels", "", "capacity levels, comma-separated (default depends on the engine)")
	limit := fs.Int("limit", 0, "capacity phase: only the first N dataset URLs")
	profLevels := fs.String("profile-levels", "", "profile levels (default 1-8, then ~1.5x steps up to the capacity limit plus one)")
	profSeconds := fs.Int("profile-seconds", 90, "how long each profile level sends requests")
	profWarmup := fs.Int("profile-warmup", 20, "seconds at the start of each profile level left out of CPU, memory and pages/s")
	profMin := fs.Int("profile-min-urls", 20, "each profile level scrapes at least this many URLs")
	timeoutMS := fs.Int("timeout-ms", 30000, "per-page timeout")
	reserveMB := fs.Int("reserve-mb", 768, "host memory kept outside the engine container")
	readyTimeout := fs.Int("ready-timeout", 0, "seconds to wait for the engine to answer (default depends on the engine)")
	baseline := fs.Int("baseline-level", -1, "first level the latency rule compares against (default depends on the engine)")
	dataset := fs.String("dataset", "", "dataset CSV (default dataset/firecrawl-v1/scrape-content-dataset-v1.csv)")
	fs.BoolVar(&firecrawlScrapeOnly, "firecrawl-scrape-only", false,
		"Firecrawl: start only the API and browser service, without the queue, extract and NuQ workers that /v1/scrape never uses")
	fs.Usage = usageFor(fs, "bench ramp --engine <name> [flags]",
		"Runs the benchmark on THIS machine with Docker. `bench run` uses it on remote machines.\n"+
			"Phase 1 (capacity): all dataset URLs at rising concurrency until the engine fails.\n"+
			"Phase 2 (profile): fixed-length runs at 1, 2, 3 ... concurrent pages, measuring CPU and memory.")
	fs.Parse(args)
	def, ok := engineDefaults[*engine]
	if !ok {
		fs.Usage()
		fatalf("--engine must be one of %s", strings.Join(engines, ", "))
	}
	doCap, doProf := strings.Contains(*phases, "capacity"), strings.Contains(*phases, "profile")
	if !doCap && !doProf {
		fatalf("--phases must name capacity, profile, or both")
	}
	if *profWarmup >= *profSeconds {
		fatalf("--profile-warmup must be shorter than --profile-seconds")
	}
	if *levels == "" {
		*levels = def.levels
	}
	if *readyTimeout == 0 {
		*readyTimeout = def.readyTimeout
	}
	if *baseline < 0 {
		*baseline = def.baselineLevel
	}
	if *dataset == "" {
		*dataset = findDataset()
	}
	if *out == "" {
		*out = filepath.Join(matrixDir(), "local", *engine)
	}
	all, err := loadTasks(*dataset, 0)
	if err != nil {
		fatalf("%v", err)
	}
	o := rampOpts{engine: *engine, image: *image, memory: hostMemoryBytes() - int64(*reserveMB)<<20,
		ready: time.Duration(*readyTimeout) * time.Second, timeoutMS: *timeoutMS}

	result := map[string]any{}
	if doCap {
		os.RemoveAll(*out)
		tasks := all
		if *limit > 0 && *limit < len(all) {
			tasks = all[:*limit]
		}
		result = capacityPhase(o, tasks, parseLevels(*levels), *baseline, *out)
	} else {
		b, err := os.ReadFile(filepath.Join(*out, "ramp.json"))
		if err == nil {
			json.Unmarshal(b, &result)
		} else if *profLevels == "" {
			fatalf("no capacity results in %s: run the capacity phase first or pass --profile-levels", *out)
		}
		os.RemoveAll(filepath.Join(*out, "profile"))
	}
	if doProf {
		lv := profileLevels(int(num(result["max_sustainable_concurrency"])))
		if *profLevels != "" {
			lv = parseLevels(*profLevels)
		}
		result["profile"] = profilePhase(o, all, lv, time.Duration(*profSeconds)*time.Second,
			time.Duration(*profWarmup)*time.Second, *profMin, filepath.Join(*out, "profile"))
	} else {
		delete(result, "profile")
	}
	result["engine"] = *engine
	if firecrawlScrapeOnly {
		result["variant"] = "scrape-only"
	}
	writeJSON(filepath.Join(*out, "ramp.json"), result)
	say("")
	if doCap {
		say("CAPACITY")
		printTable(result["levels"].([]map[string]any))
		say("")
	}
	if doProf {
		say("RESOURCE PROFILE (steady state, after %ds warm-up)", *profWarmup)
		printProfile(result["profile"].(map[string]any)["levels"].([]map[string]any))
		say("")
	}
	say("== %s: max sustainable concurrency %v; results in %s", *engine, orDash(result["max_sustainable_concurrency"]), filepath.Join(*out, "ramp.json"))
}

func capacityPhase(o rampOpts, tasks []Task, lv []int, baseline int, out string) map[string]any {
	say("== %s CAPACITY: %d URLs per level, levels %s, container memory %s, %d vCPU", o.engine, len(tasks), joinInts(lv), gb(o.memory), runtime.NumCPU())
	var records []map[string]any
	var best, base map[string]any
	for _, level := range lv {
		dir := filepath.Join(out, fmt.Sprintf("level-%d", level))
		say("== %s capacity level %d: starting a fresh container", o.engine, level)
		rec, crashed := inContainer(o, level, dir, func(endpoint string, mon *Monitor) map[string]any {
			say("   scraping %d URLs, %d at a time", len(tasks), level)
			return runLevel(o.engine, endpoint, tasks, level, o.timeoutMS, dir, mon)
		})
		if _, decided := rec["passed"]; !decided {
			var reasons []string
			if crashed {
				reasons = append(reasons, "engine crashed or ran out of memory")
			}
			if best != nil && num(rec["success_rate"]) < num(best["success_rate"])-0.10 {
				reasons = append(reasons, "success fell more than 10 points below the best level")
			}
			if base != nil && num(rec["latency_ms_p50_all"]) > 3*num(base["latency_ms_p50_all"]) {
				reasons = append(reasons, "median latency more than 3x the first level")
			}
			rec["passed"] = len(reasons) == 0
			rec["reason"] = nil
			if len(reasons) > 0 {
				rec["reason"] = strings.Join(reasons, "; ")
			}
			if len(reasons) == 0 && best != nil && num(rec["pages_per_second_this_run"]) < 0.8*num(best["pages_per_second_this_run"]) {
				rec["reason"], rec["plateau"] = "throughput fell below 80% of the best level", true
			}
		}
		writeJSON(filepath.Join(dir, "level.json"), rec)
		records = append(records, rec)
		printLevel(rec, dir)
		if rec["passed"] != true || rec["plateau"] == true {
			break
		}
		if base == nil && level >= baseline {
			base = rec
		}
		if best == nil || num(rec["pages_per_second_this_run"]) > num(best["pages_per_second_this_run"]) {
			best = rec
		}
	}
	var maxLevel any
	for _, r := range records {
		if r["passed"] == true {
			maxLevel = r["level"]
		}
	}
	result := map[string]any{"urls": len(tasks), "max_sustainable_concurrency": maxLevel,
		"best_throughput_level": nil, "best_pages_per_second": nil, "levels": records}
	if best != nil {
		result["best_throughput_level"], result["best_pages_per_second"] = best["level"], best["pages_per_second_this_run"]
	}
	say("== %s capacity: max sustainable concurrency %v", o.engine, orDash(maxLevel))
	return result
}

func profilePhase(o rampOpts, tasks []Task, lv []int, window, warmup time.Duration, minURLs int, out string) map[string]any {
	say("== %s RESOURCE PROFILE: levels %s, %s each (first %s not measured), about %s in total",
		o.engine, joinInts(lv), dur(window), dur(warmup), dur(time.Duration(len(lv))*(window+20*time.Second)))
	var records []map[string]any
	for _, level := range lv {
		dir := filepath.Join(out, fmt.Sprintf("level-%d", level))
		say("== %s profile level %d: starting a fresh container", o.engine, level)
		rec, crashed := inContainer(o, level, dir, func(endpoint string, mon *Monitor) map[string]any {
			say("   %d at a time for %s", level, dur(window))
			return runProfileLevel(o.engine, endpoint, tasks, level, o.timeoutMS, window, warmup, minURLs, dir, mon)
		})
		if _, decided := rec["passed"]; !decided {
			rec["passed"], rec["reason"] = !crashed, nil
			if crashed {
				rec["reason"] = "engine crashed or ran out of memory"
			}
		}
		writeJSON(filepath.Join(dir, "level.json"), rec)
		records = append(records, rec)
		if rec["passed"] == true {
			say("   level %d: cpu %.2f cores (peak %.2f), mem %s (peak %s), %.2f pages/s, success %s",
				level, num(rec["cpu_cores_avg"]), num(rec["cpu_cores_peak"]), gb(int64(num(rec["mem_bytes_avg"]))),
				gb(int64(num(rec["mem_bytes_peak"]))), num(rec["pages_per_second_this_run"]), pctf(num(rec["success_rate"])))
			continue
		}
		printLevel(rec, dir)
		break
	}
	return map[string]any{"seconds": window.Seconds(), "warmup_seconds": warmup.Seconds(), "levels": records}
}

// runProfileLevel keeps `concurrency` requests in flight for window, cycling through tasks in dataset order.
// CPU, average memory and pages/s cover only the steady part after warmup; peak memory covers the whole run.
func runProfileLevel(engine, endpoint string, tasks []Task, concurrency, timeoutMS int, window, warmup time.Duration, minURLs int, out string, mon *Monitor) map[string]any {
	type timed struct {
		Row
		done time.Duration
	}
	var mu sync.Mutex
	var rows []timed
	var idx []int
	var next, finished, answered atomic.Int64
	start := time.Now()
	stopProgress := make(chan struct{})
	go func() {
		for {
			select {
			case <-stopProgress:
				return
			case <-time.After(10 * time.Second):
			}
			el, of := time.Since(start), "/"+dur(window)
			if el > window {
				of = " (finishing requests in flight)"
			}
			say("     %s%s  %d done  answered %s  cpu %.2f cores  mem %s (peak %s)", dur(el), of,
				finished.Load(), pct(answered.Load(), finished.Load()), mon.lastCPU.Load().(float64),
				gb(mon.lastMem.Load()), gb(mon.peakSeen.Load()))
		}
	}()
	var wg sync.WaitGroup
	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1)) - 1
				if time.Since(start) >= window && i >= minURLs {
					return
				}
				t := tasks[i%len(tasks)]
				r := scrape(endpoint, engine, t.URL, timeoutMS)
				r.ID = t.ID
				if r.answered() {
					answered.Add(1)
				}
				finished.Add(1)
				mu.Lock()
				rows = append(rows, timed{r, time.Since(start)})
				idx = append(idx, i%len(tasks))
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	close(stopProgress)

	steady := mon.window(warmup.Seconds(), window.Seconds())
	delete(steady, "mem_bytes_peak")
	var lat []float64
	var success, inWindow int
	var f1 float64
	f, _ := os.Create(filepath.Join(out, "results.jsonl"))
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	for i := range rows {
		rows[i].score(tasks[idx[i]])
		enc.Encode(rows[i].Row)
		lat = append(lat, rows[i].LatencyMS)
		if rows[i].Success {
			success++
		}
		f1 += rows[i].F1
		if rows[i].done >= warmup && rows[i].done < window {
			inWindow++
		}
	}
	f.Close()
	n := float64(max(len(rows), 1))
	summary := map[string]any{
		"engine": engine, "concurrency": concurrency, "urls": len(rows),
		"success_rate": float64(success) / n, "avg_f1": f1 / n,
		"latency_ms_p50_all": percentile(lat, 0.5), "latency_ms_p95_all": percentile(lat, 0.95),
		"wall_seconds_this_run":     time.Since(start).Seconds(),
		"pages_per_second_this_run": float64(inWindow) / (window - warmup).Seconds(),
		"steady_from_seconds":       warmup.Seconds(), "steady_to_seconds": window.Seconds(),
	}
	for k, v := range steady {
		summary[k] = v
	}
	writeJSON(filepath.Join(out, "summary.json"), summary)
	return summary
}

func findDataset() string {
	if p := "dataset/firecrawl-v1/scrape-content-dataset-v1.csv"; fileExists(p) {
		return p
	}
	return firecrawlDataset()
}

func printLevel(r map[string]any, dir string) {
	if r["passed"] == true && r["plateau"] != true {
		say("   PASSED level %v: success %s, %.2f pages/s, p50 %s, cpu avg %.2f cores, mem peak %s",
			r["level"], pctf(num(r["success_rate"])), num(r["pages_per_second_this_run"]),
			ms(num(r["latency_ms_p50_all"])), num(r["cpu_cores_avg"]), gb(int64(num(r["mem_bytes_peak"]))))
		return
	}
	verdict := "STOPPED"
	if r["passed"] != true {
		verdict = "FAILED"
	}
	say("   %s at level %v: %v", verdict, r["level"], r["reason"])
	if r["success_rate"] != nil {
		say("   success %s, %.2f pages/s, p50 %s, mem peak %s, oom-killed %v",
			pctf(num(r["success_rate"])), num(r["pages_per_second_this_run"]), ms(num(r["latency_ms_p50_all"])),
			gb(int64(num(r["mem_bytes_peak"]))), r["oom_killed"])
	}
	if b, err := os.ReadFile(filepath.Join(dir, "engine.log")); err == nil {
		lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
		say("   last engine log lines (%s):", filepath.Join(dir, "engine.log"))
		for _, l := range lines[max(0, len(lines)-15):] {
			if len(l) > 200 {
				l = l[:200] + "…"
			}
			say("   | %s", l)
		}
	}
}

func printTable(records []map[string]any) {
	say("%-6s %-7s %-8s %-10s %-9s %-9s %-9s %-9s %s", "level", "result", "success", "pages/s", "p50", "p95", "cpu avg", "mem peak", "reason")
	for _, r := range records {
		res := "pass"
		if r["passed"] != true {
			res = "FAIL"
		} else if r["plateau"] == true {
			res = "plateau"
		}
		if r["success_rate"] == nil {
			say("%-6v %-7s %s", r["level"], res, orDash(r["reason"]))
			continue
		}
		say("%-6v %-7s %-8s %-10.2f %-9s %-9s %-9.2f %-9s %s", r["level"], res, pctf(num(r["success_rate"])),
			num(r["pages_per_second_this_run"]), ms(num(r["latency_ms_p50_all"])), ms(num(r["latency_ms_p95_all"])),
			num(r["cpu_cores_avg"]), gb(int64(num(r["mem_bytes_peak"]))), orDash(r["reason"]))
	}
}

func printProfile(records []map[string]any) {
	say("%-6s %-7s %-10s %-10s %-10s %-10s %-9s %s", "level", "result", "cpu avg", "cpu peak", "mem avg", "mem peak", "pages/s", "success")
	for _, r := range records {
		if r["success_rate"] == nil {
			say("%-6v FAIL    %s", r["level"], orDash(r["reason"]))
			continue
		}
		res := "ok"
		if r["passed"] != true {
			res = "FAIL"
		}
		say("%-6v %-7s %-10.2f %-10.2f %-10s %-10s %-9.2f %s", r["level"], res, num(r["cpu_cores_avg"]), num(r["cpu_cores_peak"]),
			gb(int64(num(r["mem_bytes_avg"]))), gb(int64(num(r["mem_bytes_peak"]))), num(r["pages_per_second_this_run"]), pctf(num(r["success_rate"])))
	}
}
