package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	http "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
	"golang.org/x/net/html/charset"
)

const maxHTMLBytes = 16 << 20

var chromeHeaders = http.Header{
	"sec-ch-ua":                 {`"Chromium";v="146", "Not(A:Brand";v="24", "Google Chrome";v="146"`},
	"sec-ch-ua-mobile":          {"?0"},
	"sec-ch-ua-platform":        {`"macOS"`},
	"upgrade-insecure-requests": {"1"},
	"user-agent":                {"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36"},
	"accept":                    {"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8"},
	"sec-fetch-site":            {"none"},
	"sec-fetch-mode":            {"navigate"},
	"sec-fetch-user":            {"?1"},
	"sec-fetch-dest":            {"document"},
	"accept-encoding":           {"gzip, deflate, br, zstd"},
	"accept-language":           {"en-US,en;q=0.9"},
	"priority":                  {"u=0, i"},
	http.HeaderOrderKey: {"sec-ch-ua", "sec-ch-ua-mobile", "sec-ch-ua-platform", "upgrade-insecure-requests", "user-agent",
		"accept", "sec-fetch-site", "sec-fetch-mode", "sec-fetch-user", "sec-fetch-dest", "accept-encoding", "accept-language", "priority"},
}

func needsStatic(r Request, doc *Document, err error) bool {
	// Static HTML cannot satisfy a selector the browser was still waiting for.
	if err != nil {
		return r.WaitSelector == "" && !strings.Contains(err.Error(), "RobotsBlocked")
	}
	return doc.Metadata.StatusCode >= 400 || challenged(doc)
}

var challengePhrases = []string{"access denied", "attention required", "just a moment", "verify you are a human",
	"are you a robot", "are you human", "captcha", "unusual traffic", "request blocked"}

// challenged reports whether a short page looks like a bot challenge served with a success status.
func challenged(doc *Document) bool {
	if len(doc.Markdown) > 2000 {
		return false
	}
	text := strings.ToLower(doc.Metadata.Title + " " + doc.Markdown)
	for _, phrase := range challengePhrases {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}

// fetchStatic fetches a page with Chrome's TLS and HTTP/2 fingerprint and converts its HTML without running scripts.
func (b *Browser) fetchStatic(ctx context.Context, r Request) (*Document, error) {
	client, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(),
		tls_client.WithClientProfile(profiles.Chrome_146), tls_client.WithDialer(*publicDialer(b.AllowPrivate)),
		tls_client.WithDisableHttp3(), tls_client.WithInsecureSkipVerify(), tls_client.WithTimeoutSeconds(20),
		tls_client.WithCookieJar(tls_client.NewCookieJar()))
	if err != nil {
		return nil, err
	}
	defer client.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header = chromeHeaders.Clone()
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()
	contentType := resp.Header.Get("Content-Type")
	if mime := strings.ToLower(contentType); mime != "" && !strings.Contains(mime, "html") && !strings.Contains(mime, "xml") && !strings.HasPrefix(mime, "text/") {
		return nil, fmt.Errorf("unsupported content type %q", contentType)
	}
	body, err := charset.NewReader(io.LimitReader(resp.Body, maxHTMLBytes+1), contentType)
	if err != nil {
		return nil, fmt.Errorf("decode HTML: %w", err)
	}
	raw, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("read HTML: %w", err)
	}
	if len(raw) > maxHTMLBytes {
		return nil, errors.New("HTML exceeds 16 MiB")
	}
	root, err := html.Parse(strings.NewReader(string(raw)))
	if err != nil {
		return nil, fmt.Errorf("parse HTML: %w", err)
	}
	page := resp.Request.URL
	var title string
	var hrefs []string
	var content *html.Node
	walk(root, func(n *html.Node) {
		switch n.DataAtom {
		case atom.Title:
			if title == "" && n.FirstChild != nil {
				title = strings.TrimSpace(n.FirstChild.Data)
			}
		case atom.Base:
			if base, err := page.Parse(attr(n, "href")); err == nil && attr(n, "href") != "" {
				page = base
			}
		case atom.A:
			if link, err := page.Parse(strings.TrimSpace(attr(n, "href"))); err == nil && attr(n, "href") != "" &&
				(link.Scheme == "http" || link.Scheme == "https") {
				link.Fragment = ""
				hrefs = append(hrefs, link.String())
			}
		case atom.Body:
			content = n
		}
	})
	if content == nil {
		return nil, errors.New("page has no body")
	}
	strip(content, r.OnlyMainContent != nil && *r.OnlyMainContent)
	md, err := newConverter().ConvertNode(content, converter.WithDomain(resp.Request.URL.String()))
	if err != nil {
		return nil, fmt.Errorf("convert Markdown: %w", err)
	}
	return newDocument(string(md), hrefs, title, resp.Request.URL.String(), resp.StatusCode, "http")
}

func walk(n *html.Node, visit func(*html.Node)) {
	if n.Type == html.ElementNode {
		visit(n)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, visit)
	}
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Namespace == "" && a.Key == key {
			return a.Val
		}
	}
	return ""
}

func matches(n *html.Node, selectors []string) bool {
	for _, sel := range selectors {
		switch sel[0] {
		case '#':
			if attr(n, "id") == sel[1:] {
				return true
			}
		case '.':
			for _, class := range strings.Fields(attr(n, "class")) {
				if class == sel[1:] {
					return true
				}
			}
		default:
			if n.Data == sel {
				return true
			}
		}
	}
	return false
}

func contains(n *html.Node, selectors []string) bool {
	found := false
	for c := n.FirstChild; c != nil && !found; c = c.NextSibling {
		walk(c, func(d *html.Node) { found = found || matches(d, selectors) })
	}
	return found
}

// strip removes the same nodes the browser extraction removes.
func strip(n *html.Node, main bool) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		if c.Type == html.CommentNode {
			n.RemoveChild(c)
		} else if c.Type == html.ElementNode {
			switch {
			case c.DataAtom == atom.Script, c.DataAtom == atom.Style, c.DataAtom == atom.Noscript, c.DataAtom == atom.Template,
				c.DataAtom == atom.Meta, main && matches(c, nonMainSelectors) && !contains(c, keepMainSelectors):
				n.RemoveChild(c)
			default:
				strip(c, main)
			}
		}
		c = next
	}
}
