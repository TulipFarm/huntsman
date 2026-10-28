package engine

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Key is a named API key. Request logs carry the name, never the token.
type Key struct{ Name, Token string }

var keyName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// ParseKeys reads HUNTSMAN_TOKEN (one key, named "default") or HUNTSMAN_TOKENS ("name:token,..."). Setting both is an
// error so a forgotten single token cannot outlive a key rotation.
func ParseKeys(single, named string) ([]Key, error) {
	single, named = strings.TrimSpace(single), strings.TrimSpace(named)
	if single != "" && named != "" {
		return nil, errors.New("set HUNTSMAN_TOKEN or HUNTSMAN_TOKENS, not both")
	}
	if single != "" {
		return []Key{{"default", single}}, nil
	}
	var keys []Key
	names, tokens := map[string]bool{}, map[string]bool{}
	for _, entry := range strings.Split(named, ",") {
		if entry = strings.TrimSpace(entry); entry == "" {
			continue
		}
		name, token, ok := strings.Cut(entry, ":")
		if !ok || !keyName.MatchString(name) || token == "" {
			return nil, errors.New("HUNTSMAN_TOKENS entries must be name:token, with names of lowercase letters, digits, - or _")
		}
		if names[name] || tokens[token] {
			return nil, fmt.Errorf("HUNTSMAN_TOKENS repeats key %q or its token", name)
		}
		names[name], tokens[token] = true, true
		keys = append(keys, Key{name, token})
	}
	return keys, nil
}

// authenticate returns the name of the key in the Authorization header. It compares against every key so the time
// taken does not reveal which one matched.
func (s *Service) authenticate(header string) (string, bool) {
	if len(s.Keys) == 0 {
		return "", true
	}
	name := ""
	for _, k := range s.Keys {
		if subtle.ConstantTimeCompare([]byte(header), []byte("Bearer "+k.Token)) == 1 {
			name = k.Name
		}
	}
	return name, name != ""
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

var searchOptions = map[string]func(string) bool{
	"time_range": func(v string) bool { return v == "day" || v == "week" || v == "month" || v == "year" },
	"pageno":     func(v string) bool { n, err := strconv.Atoi(v); return err == nil && n >= 1 && n <= 10 },
	"safesearch": func(v string) bool { return v == "0" || v == "1" || v == "2" },
	"language":   func(v string) bool { return len(v) <= 16 },
	"categories": func(v string) bool { return len(v) <= 100 },
}

const searchLimit = 2 << 20

// search forwards GET /v1/search to SearXNG's JSON API, so clients that can reach Huntsman need no route to SearXNG.
func (s *Service) search(w http.ResponseWriter, r *http.Request) {
	if s.SearXNG == "" {
		failure(w, 404, errors.New("search is not configured"))
		return
	}
	in := r.URL.Query()
	q := strings.TrimSpace(in.Get("q"))
	if q == "" || len(q) > 500 {
		failure(w, 400, errors.New("q must be 1..500 bytes"))
		return
	}
	out := url.Values{"q": {q}, "format": {"json"}}
	for name := range in {
		if name == "q" || name == "format" {
			continue
		}
		valid, known := searchOptions[name]
		if !known || !valid(in.Get(name)) {
			failure(w, 400, fmt.Errorf("invalid search option %q", name))
			return
		}
		out.Set(name, in.Get(name))
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.SearXNG+"/search?"+out.Encode(), nil)
	if err != nil {
		failure(w, 500, err)
		return
	}
	req.Header.Set("Accept", "application/json")
	resp, err := searchClient.Do(req)
	if err != nil {
		slog.Warn("search failed", "error", err)
		failure(w, 502, errors.New("search unavailable"))
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, searchLimit+1))
	switch {
	case resp.StatusCode == 429:
		w.Header().Set("Retry-After", "1")
		failure(w, 429, errors.New("search is rate limited"))
	case err != nil || resp.StatusCode != 200 || len(body) > searchLimit || !json.Valid(body):
		slog.Warn("search failed", "status", resp.StatusCode, "bytes", len(body), "error", err)
		failure(w, 502, errors.New("search unavailable"))
	default:
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}
}

var searchClient = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
