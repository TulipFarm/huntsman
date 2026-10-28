package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPContract(t *testing.T) {
	svc := NewService(testStore(t, 10), time.Hour, 1, []Key{{"test", "test-key"}})
	svc.Start(context.Background(), []Renderer{&fakeRenderer{}})
	defer svc.Close()
	for _, tc := range []struct {
		name, body, token string
		status            int
	}{
		{"unauthorized", `{"url":"https://example.com"}`, "", 401},
		{"invalid scheme", `{"url":"file:///etc/passwd"}`, "test-key", 400},
		{"unknown option", `{"url":"https://example.com","actions":[]}`, "test-key", 400},
		{"trailing JSON", `{"url":"https://example.com"} {}`, "test-key", 400},
		{"unsupported format", `{"url":"https://example.com","formats":["screenshot"]}`, "test-key", 400},
		{"invalid age", `{"url":"https://example.com","maxAge":-1}`, "test-key", 400},
		{"valid scrape", `{"url":"https://example.com","formats":["markdown"]}`, "test-key", 200},
		{"cached scrape", `{"url":"https://example.com","formats":["markdown"]}`, "test-key", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/v1/scrape", bytes.NewBufferString(tc.body))
			r.Header.Set("Authorization", "Bearer "+tc.token)
			svc.Handler().ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if tc.name == "cached scrape" {
				var result struct{ Cached bool }
				if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || !result.Cached {
					t.Fatalf("cache result: %s %v", w.Body.String(), err)
				}
			}
		})
	}
}

func TestNormalization(t *testing.T) {
	r := Request{URL: "https://EXAMPLE.COM#fragment"}
	if err := r.Normalize(time.Hour); err != nil {
		t.Fatal(err)
	}
	if r.URL != "https://example.com/" || *r.MaxAge != 3600000 {
		t.Fatalf("%+v", r)
	}
	for _, raw := range []string{"javascript:alert(1)", "ftp://example.com", "https://user:pass@example.com", "/relative"} {
		r := Request{URL: raw}
		if err := r.Normalize(time.Hour); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestParseKeys(t *testing.T) {
	keys, err := ParseKeys("", " demo:a, staging:b ,")
	if err != nil || len(keys) != 2 || keys[1] != (Key{"staging", "b"}) {
		t.Fatalf("%v %v", keys, err)
	}
	if keys, err := ParseKeys("solo", ""); err != nil || keys[0] != (Key{"default", "solo"}) {
		t.Fatalf("%v %v", keys, err)
	}
	for _, tc := range [][2]string{{"a", "x:b"}, {"", "demo"}, {"", "Demo:a"}, {"", "demo:"}, {"", "a:x,a:y"}, {"", "a:x,b:x"}} {
		if _, err := ParseKeys(tc[0], tc[1]); err == nil {
			t.Fatalf("accepted %q %q", tc[0], tc[1])
		}
	}
}

func TestNamedKeys(t *testing.T) {
	svc := NewService(testStore(t, 10), time.Hour, 1, []Key{{"production", "p"}, {"staging", "s"}})
	for _, tc := range []struct {
		token  string
		status int
	}{{"", 401}, {"x", 401}, {"p", 404}, {"s", 404}} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/v1/jobs/missing", nil)
		r.Header.Set("Authorization", "Bearer "+tc.token)
		svc.Handler().ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("token %q: status=%d", tc.token, w.Code)
		}
	}
}
