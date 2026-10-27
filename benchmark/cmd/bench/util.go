package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"
)

var sayMu sync.Mutex

// say prints one timestamped line to stdout.
func say(format string, a ...any) {
	sayMu.Lock()
	defer sayMu.Unlock()
	if format == "" {
		fmt.Println()
		return
	}
	fmt.Printf("%s %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, a...))
}

func itoa(n int) string { return strconv.Itoa(n) }

func round(v float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(v*p) / p
}

func num(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	case int64:
		return float64(x)
	}
	return 0
}

func orDash(v any) any {
	if v == nil {
		return "-"
	}
	return v
}

func dur(d time.Duration) string { return d.Round(time.Second).String() }
func gb(b int64) string          { return fmt.Sprintf("%.2f GB", float64(b)/1e9) }
func pctf(f float64) string      { return fmt.Sprintf("%.1f%%", f*100) }
func ms(v float64) string        { return fmt.Sprintf("%.1fs", v/1000) }

func pct(a, n int64) string {
	if n == 0 {
		return "-"
	}
	return fmt.Sprintf("%d (%.0f%%)", a, float64(a)*100/float64(n))
}

func writeJSON(path string, v any) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fatalf("encode %s: %v", path, err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		fatalf("%v", err)
	}
}

func writeJSONL[T any](path string, rows []T) {
	f, err := os.Create(path)
	if err != nil {
		fatalf("%v", err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, r := range rows {
		enc.Encode(r)
	}
}

func sortByRSS(rows []map[string]any) {
	sort.Slice(rows, func(i, j int) bool { return rows[i]["rss_bytes"].(int64) > rows[j]["rss_bytes"].(int64) })
}

func usageFor(fs *flag.FlagSet, synopsis, about string) func() {
	return func() {
		fmt.Fprintf(os.Stderr, "usage: %s\n\n%s\n\n", synopsis, about)
		fs.PrintDefaults()
	}
}
