package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

var ErrFull = errors.New("queue is full; retry later")
var ErrNotFound = errors.New("job not found")

type Request struct {
	URL             string   `json:"url"`
	Formats         []string `json:"formats,omitempty"`
	WaitFor         int      `json:"waitFor,omitempty"`
	WaitSelector    string   `json:"waitSelector,omitempty"`
	Timeout         int      `json:"timeout,omitempty"`
	MaxAge          *int64   `json:"maxAge,omitempty"`
	OnlyMainContent *bool    `json:"onlyMainContent,omitempty"`
}

func (r *Request) Normalize(defaultAge time.Duration) error {
	u, err := url.Parse(r.URL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return errors.New("url must be an absolute HTTP(S) URL without credentials")
	}
	u.Fragment = ""
	u.Host = strings.ToLower(u.Host)
	if u.Path == "" {
		u.Path = "/"
	}
	r.URL = u.String()
	for _, f := range r.Formats {
		if f != "markdown" {
			return fmt.Errorf("unsupported format %q; only markdown is supported", f)
		}
	}
	r.Formats = []string{"markdown"}
	if r.Timeout == 0 {
		r.Timeout = 30000
	}
	if r.Timeout < 100 || r.Timeout > 120000 || r.WaitFor < 0 || r.WaitFor >= r.Timeout {
		return errors.New("timeout must be 100..120000 ms; waitFor must be nonnegative and less than timeout")
	}
	if len(r.WaitSelector) > 2048 || len(r.URL) > 8192 {
		return errors.New("URL or selector is too long")
	}
	if r.MaxAge == nil {
		age := defaultAge.Milliseconds()
		r.MaxAge = &age
	}
	if *r.MaxAge < 0 || *r.MaxAge > (7*24*time.Hour).Milliseconds() {
		return errors.New("maxAge must be 0..604800000 ms")
	}
	if r.OnlyMainContent == nil {
		main := true
		r.OnlyMainContent = &main
	}
	return nil
}

func (r Request) key() string {
	r.MaxAge = nil
	r.Timeout = 0
	raw, _ := json.Marshal(r)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func (r Request) host() string {
	u, _ := url.Parse(r.URL)
	return u.Hostname()
}

type Document struct {
	Markdown string   `json:"markdown"`
	Links    []string `json:"links"`
	Metadata Metadata `json:"metadata"`
}

type Metadata struct {
	Title      string    `json:"title"`
	SourceURL  string    `json:"sourceURL"`
	StatusCode int       `json:"statusCode"`
	FetchedAt  time.Time `json:"fetchedAt"`
	Engine     string    `json:"engine"`
}

type Job struct {
	ID      string    `json:"id"`
	Status  string    `json:"status"`
	Request Request   `json:"-"`
	Data    *Document `json:"data,omitempty"`
	Error   string    `json:"error,omitempty"`
	Cached  bool      `json:"cached"`
	CrawlID string    `json:"crawlId,omitempty"`
	Depth   int       `json:"depth,omitempty"`
}

type CrawlRequest struct {
	URL           string  `json:"url"`
	Limit         int     `json:"limit"`
	MaxDepth      int     `json:"maxDepth"`
	ScrapeOptions Request `json:"scrapeOptions"`
}

type CrawlResult struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	Total     int    `json:"total"`
	Completed int    `json:"completed"`
	Failed    int    `json:"failed"`
	Jobs      []Job  `json:"jobs"`
	Next      *int   `json:"next,omitempty"`
}
