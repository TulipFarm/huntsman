package main

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// naiveWindowScores is the reference set arithmetic from scrape-evals.
func naiveWindowScores(content, truth []string) (float64, float64) {
	want := map[string]bool{}
	for _, t := range truth {
		want[t] = true
	}
	win := len(truth)
	var bestR, bestP float64
	for i := 0; i < max(len(content)-win+1, 1); i++ {
		set := map[string]bool{}
		for _, t := range content[i:min(i+win, len(content))] {
			set[t] = true
		}
		hits := 0
		for t := range set {
			if want[t] {
				hits++
			}
		}
		r, p := float64(hits)/float64(len(want)), float64(hits)/float64(max(len(set), 1))
		if r > bestR || (math.Abs(r-bestR) < 1e-9 && p > bestP) {
			bestR, bestP = r, p
		}
	}
	return bestR, bestP
}

func TestWindowScoresMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	vocab := []string{"a", "b", "c", "d", "e", "f", "g"}
	words := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = vocab[rng.Intn(len(vocab))]
		}
		return out
	}
	for i := 0; i < 2000; i++ {
		content, truth := words(rng.Intn(40)+1), words(rng.Intn(12)+1)
		r, p, _ := windowScores(content, truth)
		wr, wp := naiveWindowScores(content, truth)
		if math.Abs(r-wr) > 1e-9 || math.Abs(p-wp) > 1e-9 {
			t.Fatalf("content %v truth %v: got %.3f/%.3f want %.3f/%.3f", content, truth, r, p, wr, wp)
		}
	}
}

func TestScore(t *testing.T) {
	ok := 200
	r := Row{StatusCode: &ok, content: "# Hello\n\n[The **quick** brown](http://x) fox, 12/34 café"}
	r.score(Task{Truth: "the quick brown fox 12/34 café"})
	if !r.Success || r.Recall != 1 {
		t.Fatalf("got success=%v recall=%v", r.Success, r.Recall)
	}
	r = Row{StatusCode: &ok, content: "Attention Required! | Cloudflare"}
	r.score(Task{Truth: "anything"})
	if r.Success {
		t.Fatal("a block page must not count as success")
	}
}

func TestProfileLevels(t *testing.T) {
	for limit, want := range map[int]string{0: "1", 4: "1,2,3,4,5", 8: "1,2,3,4,5,6,7,8,12", 32: "1,2,3,4,5,6,7,8,12,16,24,32,48"} {
		if got := joinInts(profileLevels(limit)); got != want {
			t.Errorf("profileLevels(%d) = %s, want %s", limit, got, want)
		}
	}
}

func TestLoadTasksSplit(t *testing.T) {
	p := filepath.Join(t.TempDir(), "d.csv")
	os.WriteFile(p, []byte("id,url,truth_text,lie_text,split\n1,a,t,l,dev\n2,b,t,l,test\n3,c,t,l,test\n"), 0o644)
	if got, _ := loadTasks(p, "", 0); len(got) != 3 {
		t.Fatalf("no split: got %d rows, want 3", len(got))
	}
	got, _ := loadTasks(p, "test", 1)
	if len(got) != 1 || got[0].ID != "2" {
		t.Fatalf("split test, limit 1: got %+v", got)
	}
	noSplit := filepath.Join(t.TempDir(), "n.csv")
	os.WriteFile(noSplit, []byte("id,url,truth_text,lie_text\n1,a,t,l\n"), 0o644)
	if _, err := loadTasks(noSplit, "test", 0); err == nil {
		t.Fatal("want an error for --split on a dataset without a split column")
	}
}

func TestSearchLevels(t *testing.T) {
	for _, c := range []struct {
		limit, refine, want int
		tried               []int
	}{
		{limit: 13, refine: 2, want: 12, tried: []int{8, 16, 12, 14}},
		{limit: 100, refine: 2, want: 64, tried: []int{8, 16, 32, 64}},
		{limit: 13, refine: 0, want: 8, tried: []int{8, 16}},
		{limit: 5, refine: 2, want: 0, tried: []int{8}},
	} {
		var tried []int
		got := searchLevels([]int{8, 16, 32, 64}, c.refine, func(l int) (bool, bool) {
			tried = append(tried, l)
			return l <= c.limit, false
		})
		if got != c.want || fmt.Sprint(tried) != fmt.Sprint(c.tried) {
			t.Errorf("limit %d refine %d: got %d after %v, want %d after %v", c.limit, c.refine, got, tried, c.want, c.tried)
		}
	}
}
