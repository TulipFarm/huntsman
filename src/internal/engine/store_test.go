package engine

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testStore(t *testing.T, capacity int) *Store {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "test.db"), capacity)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}

func request(t *testing.T, url string) Request {
	t.Helper()
	r := Request{URL: url}
	if err := r.Normalize(time.Hour); err != nil {
		t.Fatal(err)
	}
	return r
}

func document(r Request) *Document {
	return &Document{Markdown: "# Complete", Links: []string{}, Metadata: Metadata{SourceURL: r.URL, FetchedAt: time.Now()}}
}

func TestQueueCoalescesAndBatchIsAtomic(t *testing.T) {
	s := testStore(t, 1)
	r := request(t, "https://example.com/one")
	a, err := s.Submit([]Request{r, r})
	if err != nil || len(a) != 2 || a[0].ID != a[1].ID {
		t.Fatalf("duplicate submission: %v %v", a, err)
	}
	_, err = s.Submit([]Request{request(t, "https://example.com/two")})
	if !errors.Is(err, ErrFull) {
		t.Fatalf("want queue full, got %v", err)
	}
	s2 := testStore(t, 1)
	_, err = s2.Submit([]Request{r, request(t, "https://example.com/two")})
	if !errors.Is(err, ErrFull) {
		t.Fatal(err)
	}
	if _, err = s2.Claim(1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("partial batch was persisted: %v", err)
	}
}

func TestCacheFreshnessForceAndOptions(t *testing.T) {
	s := testStore(t, 10)
	r := request(t, "https://example.com")
	if _, err := s.Submit([]Request{r}); err != nil {
		t.Fatal(err)
	}
	j, err := s.Claim(1)
	if err != nil {
		t.Fatal(err)
	}
	d := document(r)
	d.Metadata.FetchedAt = time.Now().Add(-2 * time.Second)
	if err := s.Finish(j, d, nil, false); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Cached(r); err != nil || got == nil {
		t.Fatalf("fresh cache: %v %v", got, err)
	}
	age := int64(1000)
	stale := r
	stale.MaxAge = &age
	if got, err := s.Cached(stale); err != nil || got != nil {
		t.Fatalf("stale cache: %v %v", got, err)
	}
	age = 0
	if got, err := s.Cached(stale); err != nil || got != nil {
		t.Fatalf("forced refresh: %v %v", got, err)
	}
	other := r
	other.WaitSelector = "#different"
	if got, err := s.Cached(other); err != nil || got != nil {
		t.Fatalf("cache variant collision: %v %v", got, err)
	}
	missing := request(t, "https://example.com/missing")
	if _, err := s.Submit([]Request{missing}); err != nil {
		t.Fatal(err)
	}
	if j, err = s.Claim(1); err != nil {
		t.Fatal(err)
	}
	d = document(missing)
	d.Metadata.StatusCode = 404
	if err := s.Finish(j, d, nil, false); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Cached(missing); err != nil || got != nil {
		t.Fatalf("error page was cached: %v %v", got, err)
	}
}

func TestRecoveryAndExclusiveStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.db")
	s, err := OpenStore(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := OpenStore(path, 10); err == nil {
		second.Close()
		t.Fatal("allowed a second process owner")
	}
	jobs, err := s.Submit([]Request{request(t, "https://example.com")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(1); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	j, err := s.Get(jobs[0].ID)
	if err != nil || j.Status != "queued" {
		t.Fatalf("running job not recovered: %+v %v", j, err)
	}
}

func TestPerHostLimitAndAtomicClaims(t *testing.T) {
	s := testStore(t, 50)
	requests := make([]Request, 20)
	for i := range requests {
		requests[i] = request(t, fmt.Sprintf("https://example.com/%d", i))
	}
	if _, err := s.Submit(requests); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var claimed atomic.Int32
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Claim(3); err == nil {
				claimed.Add(1)
			} else if !errors.Is(err, ErrNotFound) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if claimed.Load() != 3 {
		t.Fatalf("per-host limit not respected: %d", claimed.Load())
	}
}

func TestCrawlDepthLimitAndDedup(t *testing.T) {
	s := testStore(t, 100)
	r := request(t, "https://example.com")
	id, err := s.StartCrawl(CrawlRequest{URL: r.URL, Limit: 3, MaxDepth: 1, ScrapeOptions: r})
	if err != nil {
		t.Fatal(err)
	}
	j, err := s.Claim(2)
	if err != nil {
		t.Fatal(err)
	}
	d := document(r)
	d.Links = []string{"https://example.com/one", "https://example.com/one#fragment", "https://evil.example/two", "https://example.com/two", "https://example.com/three"}
	if err := s.Finish(j, d, nil, false); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		j, err = s.Claim(2)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Finish(j, d, nil, false); err != nil {
			t.Fatal(err)
		}
	}
	out, err := s.Crawl(id, 0)
	if err != nil || out.Total != 3 || out.Completed != 3 || out.Status != "completed" {
		t.Fatalf("crawl result: %+v %v", out, err)
	}
}

type fakeRenderer struct {
	calls atomic.Int32
}

func (r *fakeRenderer) Render(ctx context.Context, req Request) (*Document, error) {
	r.calls.Add(1)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(20 * time.Millisecond):
		return document(req), nil
	}
}
func (*fakeRenderer) Close() {}

func TestServiceCompletesAndCaches(t *testing.T) {
	s := testStore(t, 100)
	svc := NewService(s, time.Hour, 2, nil)
	renderer := &fakeRenderer{}
	svc.Start(context.Background(), []Renderer{renderer})
	defer svc.Close()
	r := request(t, "https://example.com")
	jobs, err := s.Submit([]Request{r, r})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	j, err := svc.wait(ctx, jobs[0].ID)
	if err != nil || j.Status != "completed" || renderer.calls.Load() != 1 {
		t.Fatalf("job: %+v %v calls=%d", j, err, renderer.calls.Load())
	}
	jobs, err = s.Submit([]Request{r})
	if err != nil || !jobs[0].Cached || jobs[0].Status != "completed" {
		t.Fatalf("cached submit: %+v %v", jobs, err)
	}
}
