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
	switch {
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

func (s *Service) scrape(w http.ResponseWriter, r *http.Request) {
	var req Request
	if err := decode(w, r, &req); err != nil {
		failure(w, 400, err)
		return
	}
	if err := req.Normalize(s.DefaultAge); err != nil {
		failure(w, 400, err)
		return
	}
	doc, err := s.Store.Cached(req)
	if err != nil {
		s.storeError(w, err)
		return
	}
	if doc != nil {
		writeJSON(w, 200, map[string]any{"success": true, "data": doc, "cached": true})
		return
	}
	jobs, err := s.Store.Submit([]Request{req})
	if err != nil {
		s.storeError(w, err)
		return
	}
	s.notify()
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(req.Timeout)*time.Millisecond)
	defer cancel()
	j, err := s.wait(ctx, jobs[0].ID)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			writeJSON(w, 202, map[string]any{"success": true, "id": jobs[0].ID, "status": "pending"})
		} else {
			s.storeError(w, err)
		}
		return
	}
	if j.Status == "failed" {
		writeJSON(w, 502, map[string]any{"success": false, "id": j.ID, "error": j.Error})
		return
	}
	writeJSON(w, 200, map[string]any{"success": true, "id": j.ID, "data": j.Data, "cached": j.Cached})
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
	if len(req.URLs) < 1 || len(req.URLs) > 1000 {
		failure(w, 400, errors.New("urls must contain 1..1000 entries"))
		return
	}
	requests := make([]Request, 0, len(req.URLs))
	for _, u := range req.URLs {
		item := req.Options
		item.URL = u
		if err := item.Normalize(s.DefaultAge); err != nil {
			failure(w, 400, err)
			return
		}
		requests = append(requests, item)
	}
	jobs, err := s.Store.Submit(requests)
	if err != nil {
		s.storeError(w, err)
		return
	}
	s.notify()
	writeJSON(w, 202, map[string]any{"success": true, "jobs": jobs})
}

func (s *Service) crawl(w http.ResponseWriter, r *http.Request) {
	var req CrawlRequest
	if err := decode(w, r, &req); err != nil {
		failure(w, 400, err)
		return
	}
	if req.Limit == 0 {
		req.Limit = 100
	}
	req.ScrapeOptions.URL = req.URL
	if err := req.ScrapeOptions.Normalize(s.DefaultAge); err != nil {
		failure(w, 400, err)
		return
	}
	req.URL = req.ScrapeOptions.URL
	if req.Limit < 1 || req.Limit > 1000 || req.MaxDepth < 0 || req.MaxDepth > 10 {
		failure(w, 400, errors.New("crawl limit must be 1..1000 and maxDepth 0..10"))
		return
	}
	id, err := s.Store.StartCrawl(req)
	if err != nil {
		s.storeError(w, err)
		return
	}
	s.notify()
	writeJSON(w, 202, map[string]any{"success": true, "id": id})
}
