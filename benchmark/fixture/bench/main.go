package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type sample struct {
	Index      int     `json:"index"`
	LatencyMS  float64 `json:"latency_ms"`
	Valid      bool    `json:"valid"`
	HTTPStatus int     `json:"http_status"`
	Error      string  `json:"error,omitempty"`
	Bytes      int     `json:"markdown_bytes"`
	Hash       string  `json:"markdown_sha256,omitempty"`
	Cached     *bool   `json:"cached,omitempty"`
}

type response struct {
	Success bool   `json:"success"`
	ID      string `json:"id"`
	Status  string `json:"status"`
	Error   string `json:"error"`
	Cached  *bool  `json:"cached"`
	Data    *struct {
		Markdown string `json:"markdown"`
	} `json:"data"`
}

type config struct {
	target, fixture, kind, mode, runID, token string
	wait                                      int
	timeout                                   time.Duration
}

func scrape(client *http.Client, cfg config, i int) sample {
	started := time.Now()
	out := sample{Index: i}
	id := i
	if cfg.mode == "duplicate" {
		id = 0
	}
	pageURL := fmt.Sprintf("%s/page/%d?kind=%s&run=%s", cfg.fixture, id, url.QueryEscape(cfg.kind), url.QueryEscape(cfg.runID))
	age := 0
	if cfg.mode == "warm" {
		age = 3600000
	}
	payload, _ := json.Marshal(map[string]any{"url": pageURL, "formats": []string{"markdown"},
		"maxAge": age, "waitFor": cfg.wait, "timeout": 60000, "onlyMainContent": true})
	ctx, cancel := context.WithTimeout(context.Background(), cfg.timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, "POST", cfg.target, bytes.NewReader(payload))
	var body response
	if err == nil {
		request.Header.Set("Content-Type", "application/json")
		if cfg.token != "" {
			request.Header.Set("Authorization", "Bearer "+cfg.token)
		}
		body, out.HTTPStatus, err = execute(client, request)
	}
	if err == nil && out.HTTPStatus == 202 && body.ID != "" {
		base, parseErr := url.Parse(cfg.target)
		if parseErr != nil {
			err = parseErr
		} else {
			base.Path = "/v1/jobs/" + body.ID
			for err == nil {
				select {
				case <-ctx.Done():
					err = ctx.Err()
				case <-time.After(50 * time.Millisecond):
					poll, makeErr := http.NewRequestWithContext(ctx, "GET", base.String(), nil)
					if makeErr != nil {
						err = makeErr
						break
					}
					if cfg.token != "" {
						poll.Header.Set("Authorization", "Bearer "+cfg.token)
					}
					body, out.HTTPStatus, err = execute(client, poll)
				}
				if body.Status == "completed" || body.Status == "failed" {
					break
				}
			}
		}
	}
	out.LatencyMS = float64(time.Since(started).Microseconds()) / 1000
	if err != nil {
		out.Error = err.Error()
		return out
	}
	out.Cached = body.Cached
	if out.HTTPStatus != 200 || body.Data == nil || body.Error != "" {
		out.Error = fmt.Sprintf("HTTP %d: %s (status=%s, data=%t)", out.HTTPStatus, body.Error, body.Status, body.Data != nil)
		return out
	}
	md := body.Data.Markdown
	out.Bytes = len(md)
	sum := sha256.Sum256([]byte(md))
	out.Hash = hex.EncodeToString(sum[:])
	heading := fmt.Sprintf("HuntsmanPage%d", id)
	if !strings.Contains(md, "# "+heading+"\n") && !strings.Contains(md, heading+"\n===") {
		out.Error = "incomplete Markdown: missing heading " + heading
		return out
	}
	required := []string{fmt.Sprintf("PayloadComplete%d", id),
		"Paragraph 23", "CompleteCode", "| TableMarker", "/page/4"}
	for _, marker := range required {
		if !strings.Contains(md, marker) {
			out.Error = "incomplete Markdown: missing " + marker
			return out
		}
	}
	out.Valid = true
	return out
}

func execute(client *http.Client, req *http.Request) (response, int, error) {
	var result response
	res, err := client.Do(req)
	if err != nil {
		return result, 0, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 5<<20))
	if err != nil {
		return result, res.StatusCode, err
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		text := string(raw)
		if len(text) > 200 {
			text = text[:200]
		}
		return result, res.StatusCode, fmt.Errorf("invalid JSON response: %s", text)
	}
	return result, res.StatusCode, nil
}

func percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	return sorted[int(math.Ceil(float64(len(sorted))*p))-1]
}

func run() error {
	target := flag.String("target", "", "scrape endpoint")
	fixture := flag.String("fixture", "http://fixture:8090", "fixture URL as seen by the scraper")
	kind := flag.String("kind", "js", "static, js, or delayed")
	mode := flag.String("mode", "cold", "cold, warm, or duplicate")
	count := flag.Int("requests", 64, "measured requests")
	concurrency := flag.Int("concurrency", 8, "concurrent clients")
	wait := flag.Int("wait-ms", 0, "same explicit waitFor for both engines")
	output := flag.String("output", "", "JSON result file")
	timeout := flag.Duration("timeout", 120*time.Second, "end-to-end request deadline")
	flag.Parse()
	if *target == "" || *count < 1 || *count > 100000 || *concurrency < 1 || *concurrency > 1000 ||
		(*mode != "cold" && *mode != "warm" && *mode != "duplicate") ||
		(*kind != "static" && *kind != "js" && *kind != "delayed") {
		return errors.New("invalid target, requests, concurrency, mode, or kind")
	}
	client := &http.Client{Transport: &http.Transport{MaxIdleConns: *concurrency + 5, MaxIdleConnsPerHost: *concurrency + 5}}
	defer client.CloseIdleConnections()
	cfg := config{target: *target, fixture: *fixture, kind: *kind, mode: *mode,
		runID: fmt.Sprint(time.Now().UnixNano()), wait: *wait, timeout: *timeout, token: os.Getenv("BENCH_TOKEN")}
	// The warm-up has a different URL from every measured cold request.
	warmup := cfg
	warmup.runID += "-warmup"
	if result := scrape(client, warmup, 0); !result.Valid {
		return fmt.Errorf("warm-up failed: %s", result.Error)
	}
	if cfg.mode == "warm" {
		for i := range *count {
			if result := scrape(client, cfg, i); !result.Valid {
				return fmt.Errorf("cache prefill %d failed: %s", i, result.Error)
			}
		}
	}
	results := make([]sample, *count)
	work := make(chan int)
	var wg sync.WaitGroup
	started := time.Now()
	for range *concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range work {
				results[i] = scrape(client, cfg, i)
			}
		}()
	}
	for i := range *count {
		work <- i
	}
	close(work)
	wg.Wait()
	duration := time.Since(started).Seconds()
	all, valid := []float64{}, []float64{}
	for _, result := range results {
		all = append(all, result.LatencyMS)
		if result.Valid {
			valid = append(valid, result.LatencyMS)
		}
	}
	report := map[string]any{
		"target": *target, "fixture": *fixture, "kind": *kind, "mode": *mode, "run_id": cfg.runID,
		"requests": *count, "concurrency": *concurrency, "wait_ms": *wait,
		"duration_seconds": duration, "valid": len(valid), "failed": *count - len(valid),
		"valid_pages_per_second": float64(len(valid)) / duration,
		"latency_ms_p50_all":     percentile(all, .5), "latency_ms_p95_all": percentile(all, .95),
		"latency_ms_p50_valid": percentile(valid, .5), "latency_ms_p95_valid": percentile(valid, .95),
		"started_at": started.UTC(), "samples": results,
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if *output != "" {
		if err := os.MkdirAll(filepath.Dir(*output), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(*output, append(data, '\n'), 0644); err != nil {
			return err
		}
	}
	fmt.Printf("%s %s c=%d valid=%d/%d %.2f pages/s p50=%.1fms p95=%.1fms\n",
		*kind, *mode, *concurrency, len(valid), *count, float64(len(valid))/duration, percentile(all, .5), percentile(all, .95))
	if len(valid) != *count {
		return fmt.Errorf("%d requests failed content validation; see result JSON", *count-len(valid))
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
