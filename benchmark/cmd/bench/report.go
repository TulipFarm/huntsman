package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var reportSizes = []string{"small", "medium", "large"}

type reportLevel struct {
	Level   int      `json:"level"`
	Passed  *bool    `json:"passed"`
	Reason  *string  `json:"reason"`
	Success *float64 `json:"success"`
	F1      *float64 `json:"f1"`
	PPS     *float64 `json:"pps"`
	P50     *float64 `json:"p50"`
	P95     *float64 `json:"p95"`
	CPU     *float64 `json:"cpu"`
	CPUPeak *float64 `json:"cpu_peak"`
	Mem     *float64 `json:"mem"`
	MemPeak *float64 `json:"mem_peak"`
}

type reportEngine struct {
	Max       *int          `json:"max"`
	BestLevel *int          `json:"best_level"`
	BestPPS   *float64      `json:"best_pps"`
	Levels    []reportLevel `json:"levels"`
	Profile   []reportLevel `json:"profile,omitempty"`
}

type reportSize struct {
	Host    json.RawMessage         `json:"host"`
	Engines map[string]reportEngine `json:"engines"`
}

type rampFile struct {
	Max       *int                         `json:"max_sustainable_concurrency"`
	BestLevel *int                         `json:"best_throughput_level"`
	BestPPS   *float64                     `json:"best_pages_per_second"`
	Levels    []map[string]json.RawMessage `json:"levels"`
	Profile   *struct {
		Levels []map[string]json.RawMessage `json:"levels"`
	} `json:"profile"`
}

func condense(lv map[string]json.RawMessage) reportLevel {
	var row reportLevel
	json.Unmarshal(lv["level"], &row.Level)
	json.Unmarshal(lv["passed"], &row.Passed)
	json.Unmarshal(lv["reason"], &row.Reason)
	row.Success = scaled(field(lv, "success_rate"), 1, 3)
	row.F1 = scaled(field(lv, "avg_f1"), 1, 3)
	row.PPS = scaled(field(lv, "pages_per_second_this_run"), 1, 3)
	row.CPU = scaled(field(lv, "cpu_cores_avg"), 1, 3)
	row.CPUPeak = scaled(field(lv, "cpu_cores_peak"), 1, 3)
	row.P50 = scaled(field(lv, "latency_ms_p50_all"), 1, 0)
	row.P95 = scaled(field(lv, "latency_ms_p95_all"), 1, 0)
	row.Mem = scaled(field(lv, "mem_bytes_avg"), 1e6, 0)
	row.MemPeak = scaled(field(lv, "mem_bytes_peak"), 1e6, 0)
	return row
}

func field(lv map[string]json.RawMessage, key string) *float64 {
	var v *float64
	if raw, ok := lv[key]; ok {
		json.Unmarshal(raw, &v)
	}
	return v
}

func scaled(v *float64, div float64, places int) *float64 {
	if v == nil {
		return nil
	}
	r := round(*v/div, places)
	if places == 0 {
		r = math.Round(*v / div)
	}
	return &r
}

// matrixSummary condenses results/matrix/<size>/<engine>/ramp.json into the report's data block.
func matrixSummary(root string) map[string]reportSize {
	out := map[string]reportSize{}
	for _, size := range reportSizes {
		host, err := os.ReadFile(filepath.Join(root, size, "host.json"))
		if err != nil {
			continue
		}
		entry := reportSize{Host: json.RawMessage(compactJSON(host)), Engines: map[string]reportEngine{}}
		for _, engine := range engines {
			b, err := os.ReadFile(filepath.Join(root, size, engine, "ramp.json"))
			if err != nil {
				continue
			}
			var ramp rampFile
			if err := json.Unmarshal(b, &ramp); err != nil {
				fatalf("%s/%s/ramp.json: %v", size, engine, err)
			}
			e := reportEngine{Max: ramp.Max, BestLevel: ramp.BestLevel, BestPPS: scaled(ramp.BestPPS, 1, 3), Levels: []reportLevel{}}
			for _, lv := range ramp.Levels {
				e.Levels = append(e.Levels, condense(lv))
			}
			if ramp.Profile != nil {
				for _, lv := range ramp.Profile.Levels {
					e.Profile = append(e.Profile, condense(lv))
				}
			}
			entry.Engines[engine] = e
		}
		out[size] = entry
	}
	return out
}

func compactJSON(b []byte) []byte {
	var v any
	if json.Unmarshal(b, &v) != nil {
		return []byte("null")
	}
	c, _ := json.Marshal(v)
	return c
}

var matrixBlock = regexp.MustCompile(`(?s)/\*MATRIX\*/.*?/\*END\*/`)

func cmdReport(args []string) {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	inject := fs.String("inject", "", "HTML report whose /*MATRIX*/…/*END*/ block is replaced (e.g. benchmark/results/index.html)")
	root := fs.String("matrix", "", "results directory (default benchmark/results/matrix)")
	fs.Usage = usageFor(fs, "bench report [--inject benchmark/results/index.html]",
		"Prints a results table per machine, or with --inject refreshes the data inside the HTML report.")
	fs.Parse(args)
	if *root == "" {
		*root = matrixDir()
	}
	data := matrixSummary(*root)
	if *inject == "" {
		printMatrix(data)
		return
	}
	b, _ := json.Marshal(data)
	html, err := os.ReadFile(*inject)
	if err != nil {
		fatalf("%v", err)
	}
	if n := len(matrixBlock.FindAllIndex(html, -1)); n != 1 {
		fatalf("%s has %d /*MATRIX*/ blocks, want 1", *inject, n)
	}
	html = matrixBlock.ReplaceAllLiteral(html, []byte("/*MATRIX*/"+string(b)+"/*END*/"))
	if err := os.WriteFile(*inject, html, 0o644); err != nil {
		fatalf("%v", err)
	}
	fmt.Printf("updated %s\n", *inject)
}

func printMatrix(data map[string]reportSize) {
	for _, size := range reportSizes {
		entry, ok := data[size]
		if !ok {
			continue
		}
		var host struct {
			VCPUs    int    `json:"vcpus"`
			MemBytes int64  `json:"mem_bytes"`
			Instance string `json:"instance_type"`
		}
		json.Unmarshal(entry.Host, &host)
		fmt.Printf("\n%s  (%s, %d vCPU, %s)\n", strings.ToUpper(size), host.Instance, host.VCPUs, gb(host.MemBytes))
		fmt.Printf("  %-10s %-9s %-12s %s\n", "engine", "max URLs", "best pages/s", "levels (success · pages/s · mem peak)")
		for _, engine := range engines {
			e, ok := entry.Engines[engine]
			if !ok {
				continue
			}
			maxS, best := "-", "-"
			if e.Max != nil {
				maxS = itoa(*e.Max)
			}
			if e.BestPPS != nil {
				best = fmt.Sprintf("%.2f", *e.BestPPS)
			}
			var parts []string
			for _, l := range e.Levels {
				mark := ""
				if l.Passed == nil || !*l.Passed {
					mark = "✗"
				}
				if l.Success == nil {
					parts = append(parts, fmt.Sprintf("%d%s:no start", l.Level, mark))
					continue
				}
				parts = append(parts, fmt.Sprintf("%d%s:%.0f%%·%.1f·%.1fG", l.Level, mark, *l.Success*100, deref(l.PPS), deref(l.MemPeak)/1000))
			}
			fmt.Printf("  %-10s %-9s %-12s %s\n", engine, maxS, best, strings.Join(parts, "  "))
		}
		var profiled bool
		for _, engine := range engines {
			e := entry.Engines[engine]
			if len(e.Profile) == 0 {
				continue
			}
			if !profiled {
				fmt.Printf("  resource profile (concurrent pages: cpu cores avg · mem avg)\n")
				profiled = true
			}
			var parts []string
			for _, l := range e.Profile {
				if l.Passed == nil || !*l.Passed {
					parts = append(parts, fmt.Sprintf("%d:✗", l.Level))
					continue
				}
				parts = append(parts, fmt.Sprintf("%d:%.1fc·%.2fG", l.Level, deref(l.CPU), deref(l.Mem)/1000))
			}
			fmt.Printf("  %-10s %s\n", engine, strings.Join(parts, "  "))
		}
	}
}

func deref(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}
