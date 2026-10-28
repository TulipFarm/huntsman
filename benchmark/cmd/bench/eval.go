package main

// Scoring follows Firecrawl's scrape-evals (MIT, Copyright (c) 2025 Firecrawl),
// and matches dataset/eval.py.

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

type Task struct {
	ID, URL, Truth, Lie string
}

func loadTasks(path, split string, limit int) ([]Task, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.LazyQuotes = true
	rows, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(rows) < 2 {
		return nil, fmt.Errorf("%s has no rows", path)
	}
	col := map[string]int{}
	for i, h := range rows[0] {
		col[strings.TrimPrefix(h, "\ufeff")] = i
	}
	for _, need := range []string{"id", "url", "truth_text", "lie_text"} {
		if _, ok := col[need]; !ok {
			return nil, fmt.Errorf("%s has no %q column", path, need)
		}
	}
	splitCol, hasSplit := col["split"]
	if split != "" && !hasSplit {
		return nil, fmt.Errorf("%s has no \"split\" column", path)
	}
	var tasks []Task
	for _, row := range rows[1:] {
		if split != "" && row[splitCol] != split {
			continue
		}
		tasks = append(tasks, Task{ID: row[col["id"]], URL: row[col["url"]], Truth: row[col["truth_text"]], Lie: row[col["lie_text"]]})
		if limit > 0 && len(tasks) == limit {
			break
		}
	}
	return tasks, nil
}

var (
	tokenRe      = regexp.MustCompile(`\p{Nd}+/\p{Nd}+|[\p{L}\p{N}_'-]+`)
	codeBlockRe  = regexp.MustCompile("```[\\s\\S]*?```")
	inlineCodeRe = regexp.MustCompile("`[^`]+`")
	imageRe      = regexp.MustCompile(`!\[([^\]]*)\]\([^)]*\)`)
	linkRe       = regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`)
	linePrefixRe = regexp.MustCompile(`(?m)^[#>\-\*\+\s]+`)
	emphasisRe   = regexp.MustCompile(`[*_]{1,3}([^*_]+)[*_]{1,3}`)
	whitespaceRe = regexp.MustCompile(`\s+`)
	blockMarkers = []string{"attention required", "cloudflare", "verify you are a human", "access denied", "bot detection",
		"datadome", "akamai bot manager", "imperva", "sucuri website firewall"}
)

func tokenize(s string) []string { return tokenRe.FindAllString(strings.ToLower(s), -1) }

func stripMarkdown(md string) string {
	if md == "" {
		return ""
	}
	t := codeBlockRe.ReplaceAllString(md, " ")
	t = inlineCodeRe.ReplaceAllString(t, " ")
	t = imageRe.ReplaceAllString(t, "$1")
	t = linkRe.ReplaceAllString(t, "$1")
	t = linePrefixRe.ReplaceAllString(t, "")
	t = emphasisRe.ReplaceAllString(t, "$1")
	t = strings.ReplaceAll(t, "|", " ")
	return strings.TrimSpace(whitespaceRe.ReplaceAllString(t, " "))
}

// windowScores slides a window as long as the truth over the content and keeps the best recall
// (ties broken by precision), counting distinct tokens like the reference set arithmetic.
func windowScores(content, truth []string) (recall, precision, f1 float64) {
	if len(content) == 0 || len(truth) == 0 {
		return 0, 0, 0
	}
	win := len(truth)
	want := map[string]bool{}
	for _, t := range truth {
		want[t] = true
	}
	counts := map[string]int{}
	distinct, hits := 0, 0
	add := func(t string, d int) {
		before := counts[t]
		counts[t] = before + d
		if before == 0 && d > 0 {
			distinct++
			if want[t] {
				hits++
			}
		} else if before+d == 0 {
			distinct--
			if want[t] {
				hits--
			}
		}
	}
	end := min(win, len(content))
	for _, t := range content[:end] {
		add(t, 1)
	}
	windows := max(len(content)-win+1, 1)
	for i := 0; i < windows; i++ {
		if i > 0 {
			add(content[i-1], -1)
			add(content[i+win-1], 1)
		}
		r := float64(hits) / float64(len(want))
		p := float64(hits) / float64(max(distinct, 1))
		if r > recall || (math.Abs(r-recall) < 1e-9 && p > precision) {
			recall, precision = r, p
		}
	}
	if recall+precision > 0 {
		f1 = 2 * precision * recall / (precision + recall)
	}
	return
}

// Row is one scraped URL, written to level-N/results.jsonl.
type Row struct {
	ID           string  `json:"id"`
	URL          string  `json:"url"`
	LatencyMS    float64 `json:"latency_ms"`
	StatusCode   *int    `json:"status_code"`
	Error        *string `json:"error"`
	Success      bool    `json:"success"`
	Recall       float64 `json:"recall"`
	Precision    float64 `json:"precision"`
	F1           float64 `json:"f1"`
	ContentChars int     `json:"content_chars"`
	content      string
}

func (r *Row) answered() bool {
	return r.StatusCode != nil && *r.StatusCode >= 200 && *r.StatusCode < 400 && r.Error == nil && r.content != ""
}

func (r *Row) score(t Task) {
	r.Recall, r.Precision, r.F1 = windowScores(tokenize(stripMarkdown(r.content)), tokenize(t.Truth))
	if len(tokenize(t.Truth)) == 0 && len(tokenize(t.Lie)) == 0 {
		r.Success = false
	} else {
		lower := strings.ToLower(r.content)
		blocked := false
		for _, m := range blockMarkers {
			if strings.Contains(lower, m) {
				blocked = true
				break
			}
		}
		r.Success = r.answered() && !blocked
	}
	r.ContentChars = len([]rune(r.content))
	r.content = ""
}

const crawl4aiToken = "bench"

func scrape(endpoint, engine, url string, timeoutMS int) Row {
	base := strings.TrimRight(endpoint, "/")
	var path string
	var body map[string]any
	headers := map[string]string{"Content-Type": "application/json"}
	if engine == "crawl4ai" {
		// /crawl, unlike /md, reports the site's HTTP status, which scrape-evals scores. The pruning filter
		// gives the same "fit" Markdown as /md.
		path, body = "/crawl", map[string]any{"urls": []string{url}, "crawler_config": map[string]any{
			"type": "CrawlerRunConfig", "params": map[string]any{"cache_mode": "bypass",
				"markdown_generator": map[string]any{"type": "DefaultMarkdownGenerator", "params": map[string]any{
					"content_filter": map[string]any{"type": "PruningContentFilter", "params": map[string]any{}}}}}}}
		headers["Authorization"] = "Bearer " + crawl4aiToken
	} else {
		path, body = "/v1/scrape", map[string]any{"url": url, "formats": []string{"markdown"}, "timeout": timeoutMS}
		if engine == "huntsman" {
			body["maxAge"] = 0
		}
	}
	buf, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", base+path, bytes.NewReader(buf))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{Timeout: time.Duration(timeoutMS)*time.Millisecond + 30*time.Second}
	start := time.Now()
	row := Row{URL: url}
	fail := func(msg string) Row {
		row.LatencyMS = float64(time.Since(start).Microseconds()) / 1000
		row.Error = &msg
		return row
	}
	resp, err := client.Do(req)
	if err != nil {
		return fail(err.Error())
	}
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	row.LatencyMS = float64(time.Since(start).Microseconds()) / 1000
	code := resp.StatusCode
	if err != nil {
		return fail(err.Error())
	}
	var payload map[string]any
	if json.Unmarshal(raw, &payload) != nil {
		row.StatusCode = &code
		msg := fmt.Sprintf("non-JSON response (%d)", code)
		row.Error = &msg
		return row
	}
	if engine == "crawl4ai" {
		var result map[string]any
		if results, _ := payload["results"].([]any); len(results) > 0 {
			result, _ = results[0].(map[string]any)
		}
		markdown, _ := result["markdown"].(map[string]any)
		row.content, _ = markdown["fit_markdown"].(string)
		row.StatusCode = &code
		if sc, ok := result["status_code"].(float64); ok && sc != 0 {
			s := int(sc)
			row.StatusCode = &s
		}
		if code != 200 || result == nil || !truthy(result["success"]) {
			msg := firstText(result["error_message"], payload["detail"], payload["error"])
			if msg == "" {
				msg = fmt.Sprintf("HTTP %d", code)
			}
			row.Error = &msg
		}
		return row
	}
	data, _ := payload["data"].(map[string]any)
	meta, _ := data["metadata"].(map[string]any)
	if msg := firstText(payload["error"], meta["error"]); msg != "" {
		row.Error = &msg
	}
	if code == 202 && row.Error == nil {
		msg := "timed out (job still pending)"
		row.Error = &msg
	}
	if sc, ok := meta["statusCode"].(float64); ok && sc != 0 {
		s := int(sc)
		row.StatusCode = &s
	} else if truthy(payload["success"]) {
		row.StatusCode = &code
	}
	row.content, _ = data["markdown"].(string)
	return row
}

func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case float64:
		return x != 0
	}
	return true
}

func firstText(vals ...any) string {
	for _, v := range vals {
		if !truthy(v) {
			continue
		}
		if s, ok := v.(string); ok {
			return s
		}
		b, _ := json.Marshal(v)
		return string(b)
	}
	return ""
}

func percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	v := append([]float64(nil), values...)
	sort.Float64s(v)
	k := float64(len(v)-1) * p
	lo := int(k)
	hi := min(lo+1, len(v)-1)
	return v[lo] + (v[hi]-v[lo])*(k-float64(lo))
}
