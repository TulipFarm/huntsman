package main

import (
	"math"
	"math/rand"
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
