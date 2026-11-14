package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

func TestMCP(t *testing.T) {
	svc := NewService(testStore(t, 10), time.Hour, 1, []Key{{"test", "test-key"}})
	svc.MCP = true
	svc.Start(context.Background(), []Renderer{&fakeRenderer{}})
	defer svc.Close()
	srv := httptest.NewServer(svc.Handler())
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/mcp", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("unauthenticated status=%d", resp.StatusCode)
	}

	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: srv.URL + "/mcp",
		HTTPClient: &http.Client{Transport: bearer{"test-key"}}, DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	if strings.Join(names, ",") != "batch_scrape,crawl,get_crawl,get_job,scrape" {
		t.Fatalf("tools=%v", names)
	}

	call := func(name string, args map[string]any) (*mcp.CallToolResult, map[string]any) {
		t.Helper()
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var out map[string]any
		if !result.IsError {
			raw, _ := json.Marshal(result.StructuredContent)
			if err := json.Unmarshal(raw, &out); err != nil {
				t.Fatal(err)
			}
		}
		return result, out
	}

	result, out := call("scrape", map[string]any{"url": "https://example.com", "timeout": 5000})
	data, _ := out["data"].(map[string]any)
	if result.IsError || out["status"] != "completed" || data["markdown"] == "" {
		t.Fatalf("scrape: %+v %v", result.Content, out)
	}
	if result, _ := call("scrape", map[string]any{"url": "file:///etc/passwd"}); !result.IsError {
		t.Fatal("scrape accepted a file URL")
	}
	if result, _ := call("scrape", map[string]any{"url": "https://example.com", "actions": []any{}}); !result.IsError {
		t.Fatal("scrape accepted an unknown argument")
	}

	_, out = call("batch_scrape", map[string]any{"urls": []string{"https://example.com/a"}})
	jobs, _ := out["jobs"].([]any)
	if len(jobs) != 1 {
		t.Fatalf("batch_scrape: %v", out)
	}
	id := jobs[0].(map[string]any)["id"]
	if result, out := call("get_job", map[string]any{"id": id}); result.IsError || out["id"] != id {
		t.Fatalf("get_job: %+v %v", result.Content, out)
	}
	if result, _ := call("get_job", map[string]any{"id": "missing"}); !result.IsError {
		t.Fatal("get_job found a missing job")
	}

	_, out = call("crawl", map[string]any{"url": "https://example.com", "limit": 5})
	if result, out := call("get_crawl", map[string]any{"id": out["id"]}); result.IsError || out["total"] == nil {
		t.Fatalf("get_crawl: %+v %v", result.Content, out)
	}
}

func TestMCPDisabled(t *testing.T) {
	svc := NewService(testStore(t, 10), time.Hour, 1, nil)
	w := httptest.NewRecorder()
	svc.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{}`)))
	if w.Code != 404 {
		t.Fatalf("status=%d", w.Code)
	}
}
