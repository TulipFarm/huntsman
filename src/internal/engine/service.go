package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"
)

type Service struct {
	Store      *Store
	DefaultAge time.Duration
	PerHost    int
	Keys       []Key
	MCP        bool
	Version    string
	mu         sync.Mutex
	changed    chan struct{}
	wake       chan struct{}
	wg         sync.WaitGroup
	cancel     context.CancelFunc
	fatal      chan error
}

func NewService(store *Store, age time.Duration, perHost int, keys []Key) *Service {
	return &Service{Store: store, DefaultAge: age, PerHost: perHost, Keys: keys,
		changed: make(chan struct{}), wake: make(chan struct{}, 1), fatal: make(chan error, 1)}
}

func (s *Service) notify() {
	s.mu.Lock()
	close(s.changed)
	s.changed = make(chan struct{})
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Service) Start(ctx context.Context, renderers []Renderer) {
	ctx, s.cancel = context.WithCancel(ctx)
	for _, renderer := range renderers {
		s.wg.Add(1)
		go func(r Renderer) {
			defer s.wg.Done()
			defer r.Close()
			for ctx.Err() == nil {
				j, err := s.Store.Claim(s.PerHost)
				if errors.Is(err, ErrNotFound) {
					select {
					case <-ctx.Done():
					case <-s.wake:
					// Every queue change sends a wake-up; this poll is only a safety net, kept slow so idle workers cost nothing.
					case <-time.After(time.Second):
					}
					continue
				}
				if err != nil {
					s.fail(fmt.Errorf("claim job: %w", err))
					return
				}
				// Pass the wake-up on so an idle peer checks for more queued work.
				select {
				case s.wake <- struct{}{}:
				default:
				}
				doc, err := s.Store.Cached(j.Request)
				cached := doc != nil && err == nil
				if !cached && err == nil {
					jobCtx, cancel := context.WithTimeout(ctx, time.Duration(j.Request.Timeout)*time.Millisecond)
					doc, err = r.Render(jobCtx, j.Request)
					cancel()
				}
				if ctx.Err() != nil {
					return // Running jobs are recovered on the next start.
				}
				if err != nil {
					slog.Warn("scrape failed", "job", j.ID, "error", err)
				}
				if err = s.Store.Finish(j, doc, err, cached); err != nil {
					s.fail(fmt.Errorf("persist result: %w", err))
					return
				}
				s.notify()
			}
		}(renderer)
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := s.Store.Prune(); err != nil {
					s.fail(fmt.Errorf("prune storage: %w", err))
					return
				}
			}
		}
	}()
}

func (s *Service) fail(err error) {
	slog.Error("engine stopped", "error", err)
	select {
	case s.fatal <- err:
	default:
	}
	s.cancel()
	s.notify()
}

func (s *Service) Errors() <-chan error { return s.fatal }

func (s *Service) Close() {
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
}

func (s *Service) wait(ctx context.Context, id string) (Job, error) {
	for {
		s.mu.Lock()
		changed := s.changed
		s.mu.Unlock()
		j, err := s.Store.Get(id)
		if err != nil || j.Status == "completed" || j.Status == "failed" {
			return j, err
		}
		select {
		case <-ctx.Done():
			return j, ctx.Err()
		case <-changed:
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		slog.Warn("write response", "error", err)
	}
}

func failure(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]any{"success": false, "error": err.Error()})
}

func decode(w http.ResponseWriter, r *http.Request, dst any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("invalid request: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("request must contain exactly one JSON object")
	}
	return nil
}

func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		if err := s.Store.db.PingContext(r.Context()); err != nil {
			failure(w, 503, err)
			return
		}
		writeJSON(w, 200, map[string]bool{"success": true})
	})
	mux.HandleFunc("POST /v1/scrape", s.scrape)
	mux.HandleFunc("POST /v1/batch", s.batch)
	mux.HandleFunc("GET /v1/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		j, err := s.Store.Get(r.PathValue("id"))
		if err != nil {
			s.storeError(w, err)
			return
		}
		writeJSON(w, 200, j)
	})
	mux.HandleFunc("POST /v1/crawl", s.crawl)
	mux.HandleFunc("GET /v1/crawl/{id}", func(w http.ResponseWriter, r *http.Request) {
		offset := 0
		if raw := r.URL.Query().Get("offset"); raw != "" {
			var err error
			offset, err = strconv.Atoi(raw)
			if err != nil || offset < 0 {
				failure(w, 400, errors.New("offset must be a nonnegative integer"))
				return
			}
		}
		result, err := s.Store.Crawl(r.PathValue("id"), offset)
		if err != nil {
			s.storeError(w, err)
			return
		}
		writeJSON(w, 200, result)
	})
	if s.MCP {
		mux.Handle("/mcp", http.NewCrossOriginProtection().Handler(s.mcpHandler()))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			mux.ServeHTTP(w, r)
			return
		}
		start, sw := time.Now(), &statusWriter{ResponseWriter: w, status: 200}
		key, ok := s.authenticate(r.Header.Get("Authorization"))
		if ok {
			mux.ServeHTTP(sw, r)
		} else {
			failure(sw, 401, errors.New("authentication required"))
		}
		slog.Info("request", "key", key, "method", r.Method, "path", r.URL.Path, "status", sw.status,
			"ms", time.Since(start).Milliseconds())
	})
}

func (s *Service) storeError(w http.ResponseWriter, err error) {
	var invalid invalidError
	switch {
	case errors.As(err, &invalid):
		failure(w, 400, err)
	case errors.Is(err, ErrFull):
		w.Header().Set("Retry-After", "1")
		failure(w, 429, err)
	case errors.Is(err, ErrNotFound):
		failure(w, 404, err)
	default:
		slog.Error("storage error", "error", err)
		failure(w, 500, errors.New("storage operation failed"))
	}
}

// invalidError marks a request the client has to fix, as opposed to a storage failure.
type invalidError struct{ error }

func (e invalidError) Unwrap() error { return e.error }

// runScrape serves one URL from the cache or the queue and waits up to the request timeout. A cached result has no
// job ID; a job still running at the deadline comes back with status "pending".
func (s *Service) runScrape(ctx context.Context, req Request) (Job, error) {
	if err := req.Normalize(s.DefaultAge); err != nil {
		return Job{}, invalidError{err}
	}
	doc, err := s.Store.Cached(req)
	if err != nil {
		return Job{}, err
	}
	if doc != nil {
		return Job{Status: "completed", Data: doc, Cached: true}, nil
	}
	jobs, err := s.Store.Submit([]Request{req})
	if err != nil {
		return Job{}, err
	}
	s.notify()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(req.Timeout)*time.Millisecond)
	defer cancel()
	j, err := s.wait(ctx, jobs[0].ID)
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return Job{ID: jobs[0].ID, Status: "pending"}, nil
	}
	return j, err
}

func (s *Service) submitBatch(urls []string, options Request) ([]Job, error) {
	if len(urls) < 1 || len(urls) > 1000 {
		return nil, invalidError{errors.New("urls must contain 1..1000 entries")}
	}
	requests := make([]Request, 0, len(urls))
	for _, u := range urls {
		item := options
		item.URL = u
		if err := item.Normalize(s.DefaultAge); err != nil {
			return nil, invalidError{err}
		}
		requests = append(requests, item)
	}
	jobs, err := s.Store.Submit(requests)
	if err != nil {
		return nil, err
	}
	s.notify()
	return jobs, nil
}

func (s *Service) submitCrawl(req CrawlRequest) (string, error) {
	if req.Limit == 0 {
		req.Limit = 100
	}
	req.ScrapeOptions.URL = req.URL
	if err := req.ScrapeOptions.Normalize(s.DefaultAge); err != nil {
		return "", invalidError{err}
	}
	req.URL = req.ScrapeOptions.URL
	if req.Limit < 1 || req.Limit > 1000 || req.MaxDepth < 0 || req.MaxDepth > 10 {
		return "", invalidError{errors.New("crawl limit must be 1..1000 and maxDepth 0..10")}
	}
	id, err := s.Store.StartCrawl(req)
	if err != nil {
		return "", err
	}
	s.notify()
	return id, nil
}

func (s *Service) scrape(w http.ResponseWriter, r *http.Request) {
	var req Request
	if err := decode(w, r, &req); err != nil {
		failure(w, 400, err)
		return
	}
	j, err := s.runScrape(r.Context(), req)
	switch {
	case err != nil:
		s.storeError(w, err)
	case j.Status == "pending":
		writeJSON(w, 202, map[string]any{"success": true, "id": j.ID, "status": "pending"})
	case j.Status == "failed":
		writeJSON(w, 502, map[string]any{"success": false, "id": j.ID, "error": j.Error})
	case j.ID == "":
		writeJSON(w, 200, map[string]any{"success": true, "data": j.Data, "cached": true})
	default:
		writeJSON(w, 200, map[string]any{"success": true, "id": j.ID, "data": j.Data, "cached": j.Cached})
	}
}

func (s *Service) batch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URLs    []string `json:"urls"`
		Options Request  `json:"scrapeOptions"`
	}
	if err := decode(w, r, &req); err != nil {
		failure(w, 400, err)
		return
	}
	jobs, err := s.submitBatch(req.URLs, req.Options)
	if err != nil {
		s.storeError(w, err)
		return
	}
	writeJSON(w, 202, map[string]any{"success": true, "jobs": jobs})
}

func (s *Service) crawl(w http.ResponseWriter, r *http.Request) {
	var req CrawlRequest
	if err := decode(w, r, &req); err != nil {
		failure(w, 400, err)
		return
	}
	id, err := s.submitCrawl(req)
	if err != nil {
		s.storeError(w, err)
		return
	}
	writeJSON(w, 202, map[string]any{"success": true, "id": id})
}
