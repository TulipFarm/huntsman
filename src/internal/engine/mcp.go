package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type scrapeOptions struct {
	WaitFor         int    `json:"waitFor,omitempty" jsonschema:"Extra milliseconds to wait after the page goes quiet"`
	WaitSelector    string `json:"waitSelector,omitempty" jsonschema:"CSS selector to wait for before extracting"`
	Timeout         int    `json:"timeout,omitempty" jsonschema:"Page timeout in milliseconds, 100 to 120000 (default 30000)"`
	MaxAge          *int64 `json:"maxAge,omitempty" jsonschema:"Reuse a cached result up to this many milliseconds old; 0 forces a fresh fetch"`
	OnlyMainContent *bool  `json:"onlyMainContent,omitempty" jsonschema:"Keep only the main content, dropping navigation and footers (default true)"`
}

func (o scrapeOptions) request(url string) Request {
	return Request{URL: url, WaitFor: o.WaitFor, WaitSelector: o.WaitSelector, Timeout: o.Timeout, MaxAge: o.MaxAge,
		OnlyMainContent: o.OnlyMainContent}
}

type scrapeInput struct {
	URL string `json:"url" jsonschema:"Absolute http or https URL to scrape"`
	scrapeOptions
}

type batchInput struct {
	URLs          []string      `json:"urls" jsonschema:"1 to 1000 absolute http or https URLs"`
	ScrapeOptions scrapeOptions `json:"scrapeOptions,omitzero" jsonschema:"Options applied to every URL"`
}

type crawlInput struct {
	URL           string        `json:"url" jsonschema:"Absolute http or https URL to start from"`
	Limit         int           `json:"limit,omitempty" jsonschema:"Maximum pages to scrape, 1 to 1000 (default 100)"`
	MaxDepth      int           `json:"maxDepth,omitempty" jsonschema:"Maximum link depth from the start URL, 0 to 10"`
	ScrapeOptions scrapeOptions `json:"scrapeOptions,omitzero" jsonschema:"Options applied to every page"`
}

type idInput struct {
	ID string `json:"id" jsonschema:"Job ID returned by an earlier call"`
}

type crawlStatusInput struct {
	ID     string `json:"id" jsonschema:"Crawl ID returned by crawl"`
	Offset int    `json:"offset,omitempty" jsonschema:"Index of the first page to return; use next from the previous call"`
}

type jobsOutput struct {
	Jobs []Job `json:"jobs"`
}

type crawlOutput struct {
	ID string `json:"id"`
}

func (s *Service) mcpHandler() http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: "huntsman", Version: s.Version}, &mcp.ServerOptions{
		Instructions: "Huntsman fetches web pages, renders JavaScript and returns clean Markdown with links. Use scrape " +
			"for one page, batch_scrape for many known URLs and crawl to follow links across a site. batch_scrape and " +
			"crawl return immediately; poll get_job or get_crawl for results.",
	})
	additive := false
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true}
	queues := &mcp.ToolAnnotations{DestructiveHint: &additive}

	mcp.AddTool(server, &mcp.Tool{Name: "scrape", Annotations: readOnly,
		Description: "Fetch one web page and return its main content as Markdown, with links and metadata. If the " +
			"page takes longer than the timeout, the result has status pending and an id to pass to get_job."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in scrapeInput) (*mcp.CallToolResult, Job, error) {
			j, err := s.runScrape(ctx, in.request(in.URL))
			if err == nil && j.Status == "failed" {
				return nil, j, fmt.Errorf("scrape failed: %s", j.Error)
			}
			return nil, j, toolError(err)
		})
	mcp.AddTool(server, &mcp.Tool{Name: "batch_scrape", Annotations: queues,
		Description: "Queue up to 1000 URLs for scraping and return one job per URL. Fetch each result with get_job."},
		func(_ context.Context, _ *mcp.CallToolRequest, in batchInput) (*mcp.CallToolResult, jobsOutput, error) {
			jobs, err := s.submitBatch(in.URLs, in.ScrapeOptions.request(""))
			return nil, jobsOutput{jobs}, toolError(err)
		})
	mcp.AddTool(server, &mcp.Tool{Name: "crawl", Annotations: queues,
		Description: "Start crawling a site from a URL, following same-site links. Returns a crawl id; follow progress " +
			"and read pages with get_crawl."},
		func(_ context.Context, _ *mcp.CallToolRequest, in crawlInput) (*mcp.CallToolResult, crawlOutput, error) {
			id, err := s.submitCrawl(CrawlRequest{URL: in.URL, Limit: in.Limit, MaxDepth: in.MaxDepth,
				ScrapeOptions: in.ScrapeOptions.request("")})
			return nil, crawlOutput{id}, toolError(err)
		})
	mcp.AddTool(server, &mcp.Tool{Name: "get_job", Annotations: readOnly,
		Description: "Return the status of a scrape job and, once completed, its Markdown."},
		func(_ context.Context, _ *mcp.CallToolRequest, in idInput) (*mcp.CallToolResult, Job, error) {
			j, err := s.Store.Get(in.ID)
			return nil, j, toolError(err)
		})
	mcp.AddTool(server, &mcp.Tool{Name: "get_crawl", Annotations: readOnly,
		Description: "Return crawl progress and a page of scraped results. When next is set, call again with that " +
			"offset to read more."},
		func(_ context.Context, _ *mcp.CallToolRequest, in crawlStatusInput) (*mcp.CallToolResult, CrawlResult, error) {
			if in.Offset < 0 {
				return nil, CrawlResult{}, errors.New("offset must be a nonnegative integer")
			}
			result, err := s.Store.Crawl(in.ID, in.Offset)
			return nil, result, toolError(err)
		})

	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, MaxRequestBodyBytes: 1 << 20})
}

// toolError keeps storage details out of tool results, matching the REST API's 500 response.
func toolError(err error) error {
	var invalid invalidError
	if err == nil || errors.As(err, &invalid) || errors.Is(err, ErrFull) || errors.Is(err, ErrNotFound) {
		return err
	}
	slog.Error("storage error", "error", err)
	return errors.New("storage operation failed")
}
