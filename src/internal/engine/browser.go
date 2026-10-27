package engine

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/base"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/commonmark"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/table"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

//go:embed tracker.js
var trackerJS string

// readySettle is how long a page must stay quiet after its tracked work finishes.
const readySettle = 20 * time.Millisecond

// settleLimit caps how long a loaded page may keep working before its current content is extracted.
const settleLimit = 5 * time.Second

// networkGrace caps how long a page whose scripts are idle waits for requests they are not waiting on, such as analytics beacons.
const networkGrace = 500 * time.Millisecond

// nonMainSelectors and keepMainSelectors are Firecrawl's onlyMainContent rules: drop page chrome unless it wraps the main content.
var nonMainSelectors = []string{"header", "footer", "nav", "aside", ".header", ".top", ".navbar", "#header", ".footer", ".bottom",
	"#footer", ".sidebar", ".side", ".aside", "#sidebar", ".modal", ".popup", "#modal", ".overlay", ".ad", ".ads", ".advert", "#ad",
	".lang-selector", ".language", "#language-selector", ".social", ".social-media", ".social-links", "#social", ".menu",
	".navigation", "#nav", ".breadcrumbs", "#breadcrumbs", ".share", "#share", ".widget", "#widget", ".cookie", "#cookie"}

var keepMainSelectors = []string{"#main", ".swoogo-cols", ".swoogo-text", ".swoogo-table-div", ".swoogo-space", ".swoogo-alert",
	".swoogo-sponsors", ".swoogo-title", ".swoogo-tabs", ".swoogo-logo", ".swoogo-image", ".swoogo-button", ".swoogo-agenda"}

// adHosts matches the ad and analytics domains Firecrawl blocks.
var adHosts = []string{"doubleclick.net", "adservice.google.com", "googlesyndication.com", "googletagservices.com",
	"googletagmanager.com", "google-analytics.com", "adsystem.com", "adservice.com", "adnxs.com", "ads-twitter.com",
	"facebook.net", "fbcdn.net", "amazon-adsystem.com"}

type Renderer interface {
	Render(context.Context, Request) (*Document, error)
	Close()
}

type Browser struct {
	Binary       string
	Port         int
	AllowPrivate bool
	ObeyRobots   bool
	AdblockLists []string
	IdleTime     time.Duration
	parent       context.Context
	address      string
	cmd          *exec.Cmd
	done         chan error
	spare        chan session
	pdf          *http.Client
}

type docInfo struct {
	status int
	mime   string
}

// session is one CDP connection; Lightpanda allows one browser context per connection.
type session struct {
	tab    context.Context
	cancel context.CancelFunc
	err    error
}

func NewBrowser(ctx context.Context, binary string, port int, allowPrivate, obeyRobots bool, idle time.Duration) *Browser {
	return &Browser{Binary: binary, Port: port, AllowPrivate: allowPrivate, ObeyRobots: obeyRobots, IdleTime: idle, parent: ctx, pdf: pdfClient(allowPrivate)}
}

func (b *Browser) start() error {
	if b.cmd != nil {
		select {
		case err := <-b.done:
			b.Close()
			return fmt.Errorf("Lightpanda exited: %v", err)
		default:
			return nil
		}
	}
	b.Close()
	args := []string{"serve", "--host", "127.0.0.1", "--port", strconv.Itoa(b.Port),
		"--log-level", "error", "--http-max-response-size", "8388608",
		"--v8-max-heap-mb", "128", "--watchdog-ms", "10000",
		"--load-resources", "worker", "--load-resources", "iframe", "--load-resources", "stylesheet",
		"--http-max-host-open", "16", "--insecure-disable-tls-host-verification"}
	for _, host := range adHosts {
		args = append(args, "--block-urls", "*://*"+host+"/*")
	}
	if !b.AllowPrivate {
		args = append(args, "--block-private-networks")
	}
	for _, list := range b.AdblockLists {
		args = append(args, "--adblock-lists", list)
	}
	if b.ObeyRobots {
		args = append(args, "--obey-robots")
	}
	b.cmd = exec.CommandContext(b.parent, b.Binary, args...)
	b.cmd.Env = append(os.Environ(), "LIGHTPANDA_DISABLE_TELEMETRY=true", "LIGHTPANDA_DISABLE_CORE_DUMP=true")
	b.cmd.Stdout, b.cmd.Stderr = os.Stderr, os.Stderr
	if err := b.cmd.Start(); err != nil {
		b.cmd = nil
		return err
	}
	cmd := b.cmd
	b.done = make(chan error, 1)
	done := b.done
	go func() { done <- cmd.Wait() }()
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(b.Port))
	for {
		select {
		case <-b.parent.Done():
			b.Close()
			return b.parent.Err()
		case err := <-b.done:
			b.Close()
			return fmt.Errorf("starting Lightpanda: %v", err)
		case <-deadline.C:
			b.Close()
			return errors.New("Lightpanda startup exceeded 15 seconds")
		case <-tick.C:
			conn, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
			if err != nil {
				continue
			}
			conn.Close()
			b.address = "ws://" + address
			return nil
		}
	}
}

func open(parent context.Context, address string) session {
	alloc, closeAlloc := chromedp.NewRemoteAllocator(parent, address, chromedp.NoModifyURL)
	tab, closeTab := chromedp.NewContext(alloc)
	cancel := func() { closeTab(); closeAlloc() }
	timeout := time.AfterFunc(15*time.Second, cancel)
	err := chromedp.Run(tab, network.Enable(), chromedp.ActionFunc(func(ctx context.Context) error {
		_, err := page.AddScriptToEvaluateOnNewDocument(trackerJS).Do(ctx)
		return err
	}))
	timeout.Stop()
	if err != nil {
		cancel()
		return session{err: err}
	}
	return session{tab: tab, cancel: cancel}
}

// prepare opens the next session in the background so it is ready before the next render.
func (b *Browser) prepare() {
	ch := make(chan session, 1)
	b.spare = ch
	parent, address := b.parent, b.address
	go func() { ch <- open(parent, address) }()
}

func discard(ch chan session) {
	go func() {
		if s := <-ch; s.cancel != nil {
			s.cancel()
		}
	}()
}

func (b *Browser) take(ctx context.Context) (session, error) {
	if b.spare == nil {
		b.prepare()
	}
	ch := b.spare
	b.spare = nil
	select {
	case s := <-ch:
		return s, s.err
	case <-ctx.Done():
		discard(ch)
		return session{}, ctx.Err()
	}
}

func (b *Browser) Close() {
	if b.spare != nil {
		discard(b.spare)
		b.spare = nil
	}
	if b.cmd != nil && b.cmd.Process != nil {
		if err := b.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			slog.Error("stop browser", "error", err)
		}
		select {
		case <-b.done:
		case <-time.After(2 * time.Second):
		}
	}
	b.cmd, b.address = nil, ""
}

func (b *Browser) Render(ctx context.Context, r Request) (*Document, error) {
	browserCtx := ctx
	// Leave a quarter of the deadline for the fingerprint fallback, so pages that hang in Lightpanda can still be fetched.
	if deadline, ok := ctx.Deadline(); ok {
		var cancel context.CancelFunc
		browserCtx, cancel = context.WithDeadline(ctx, deadline.Add(-time.Until(deadline)/4))
		defer cancel()
	}
	doc, err := b.render(browserCtx, r)
	if ctx.Err() != nil || !needsStatic(r, doc, err) {
		return doc, err
	}
	// Bot protection often blocks or misroutes Lightpanda's fingerprint but not Chrome's, so retry the HTML without a browser.
	static, staticErr := b.fetchStatic(ctx, r)
	if staticErr == nil && (err != nil || static.Metadata.StatusCode < 400 && !challenged(static)) {
		return static, nil
	}
	return doc, err
}

func (b *Browser) render(ctx context.Context, r Request) (_ *Document, err error) {
	// Lightpanda downloads a whole PDF before reporting its response, so fetch likely PDFs directly.
	if u, parseErr := url.Parse(r.URL); parseErr == nil && strings.HasSuffix(strings.ToLower(u.Path), ".pdf") {
		if doc, pdfErr := b.renderPDF(ctx, r.URL); !errors.Is(pdfErr, errNotPDF) {
			return doc, pdfErr
		}
	}
	if err = b.start(); err != nil {
		return nil, err
	}
	s, err := b.take(ctx)
	if err != nil {
		return nil, fmt.Errorf("open CDP session: %w", err)
	}
	b.prepare()
	defer s.cancel()
	stop := context.AfterFunc(ctx, s.cancel)
	defer stop()
	tab := s.tab
	var mu sync.Mutex
	docs := map[string]docInfo{}
	var mainFrame cdp.FrameID
	var mainDoc docInfo
	pending := map[network.RequestID]bool{}
	lastNetwork := time.Now()
	chromedp.ListenTarget(tab, func(ev any) {
		mu.Lock()
		defer mu.Unlock()
		switch e := ev.(type) {
		case *network.EventRequestWillBeSent:
			if e.Type != network.ResourceTypeWebSocket && e.Type != network.ResourceTypeEventSource {
				pending[e.RequestID] = true
				lastNetwork = time.Now()
			}
		case *network.EventLoadingFinished:
			delete(pending, e.RequestID)
			lastNetwork = time.Now()
		case *network.EventLoadingFailed:
			delete(pending, e.RequestID)
			lastNetwork = time.Now()
		case *network.EventResponseReceived:
			if e.Type == network.ResourceTypeDocument {
				info := docInfo{int(e.Response.Status), mimeType(e.Response)}
				docs[e.Response.URL] = info
				if mainFrame == "" {
					mainFrame = e.FrameID
				}
				if e.FrameID == mainFrame {
					mainDoc = info
				}
			}
		}
	})
	// The page URL can differ from the response URL (for example after history.replaceState), so fall back to the main frame.
	document := func(href string) docInfo {
		mu.Lock()
		defer mu.Unlock()
		if info, ok := docs[href]; ok {
			return info
		}
		return mainDoc
	}
	err = chromedp.Run(tab, chromedp.Navigate(r.URL))
	if err != nil && strings.HasSuffix(err.Error(), "Shutdown") {
		// A script navigated away while the page loaded; Lightpanda reports that as Shutdown, so follow it like a redirect.
		err = waitForDocument(tab)
	}
	var href string
	if err == nil {
		err = chromedp.Run(tab, chromedp.Location(&href))
	}
	if err != nil {
		return nil, fmt.Errorf("navigate: %w", err)
	}
	info := document(href)
	if info.mime == "application/pdf" && info.status < 400 {
		return b.renderPDF(ctx, href)
	}
	// Error pages (often bot challenges that never go idle) are extracted as loaded, without waiting for scripts.
	if info.status < 400 {
		if r.WaitFor > 0 {
			timer := time.NewTimer(time.Duration(r.WaitFor) * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
		selector, _ := json.Marshal(r.WaitSelector)
		// Pages the tracker could not enter (for example plain text) fall back to a fixed quiet period.
		ready := fmt.Sprintf(`window.__huntsman ? window.__huntsman.ready(%d, %s) : new Promise(r => setTimeout(() => r("untracked"), %d))`,
			readySettle.Milliseconds(), selector, b.IdleTime.Milliseconds())
		awaitPromise := func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) }
		settle := tab
		if r.WaitSelector == "" {
			var cancelSettle context.CancelFunc
			settle, cancelSettle = context.WithTimeout(tab, settleLimit)
			defer cancelSettle()
		}
		var idleSince time.Time
		for {
			var reason string
			if err = chromedp.Run(settle, chromedp.Evaluate(ready, &reason, awaitPromise)); err != nil {
				if ctx.Err() == nil && settle.Err() != nil {
					break
				}
				if !strings.Contains(err.Error(), "Execution context was destroyed") {
					return nil, fmt.Errorf("readiness: %w", err)
				}
				// The page navigated during the check; wait for the new document and check again.
				if err = waitForDocument(tab); err != nil {
					return nil, fmt.Errorf("readiness: %w", err)
				}
				continue
			}
			if idleSince.IsZero() {
				idleSince = time.Now()
			}
			mu.Lock()
			quiet := len(pending) == 0 && time.Since(lastNetwork) >= readySettle
			mu.Unlock()
			if reason == "selector" || quiet || (reason == "idle" && time.Since(idleSince) >= networkGrace) {
				break
			}
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("page readiness: %w", ctx.Err())
			case <-settle.Done():
			case <-time.After(5 * time.Millisecond):
			}
			if ctx.Err() == nil && settle.Err() != nil {
				break
			}
		}
	}
	var pageData struct {
		HTML  string   `json:"html"`
		Title string   `json:"title"`
		URL   string   `json:"url"`
		Links []string `json:"links"`
	}
	main := r.OnlyMainContent != nil && *r.OnlyMainContent
	exclude, _ := json.Marshal(map[bool]string{true: strings.Join(nonMainSelectors, ","), false: ""}[main])
	keep, _ := json.Marshal(strings.Join(keepMainSelectors, ","))
	script := fmt.Sprintf(`(() => {
const source = document.body || document.documentElement;
if (!source) throw new Error("page has no body");
const node = source.cloneNode(true);
node.querySelectorAll('script,style,noscript,template,meta').forEach(n => n.remove());
const exclude = %s, keep = %s;
if (exclude) node.querySelectorAll(exclude).forEach(n => { if (!n.querySelector(keep)) n.remove(); });
const links = Array.from(document.querySelectorAll('a[href]')).map(a => a.href).filter(u => /^https?:\/\//.test(u));
return {html: node.outerHTML, title: document.title, url: location.href, links: links};
})()`, exclude, keep)
	if err = chromedp.Run(tab, chromedp.Evaluate(script, &pageData)); err != nil {
		return nil, fmt.Errorf("extract HTML: %w", err)
	}
	if len(pageData.HTML) > 16<<20 {
		return nil, errors.New("rendered HTML exceeds 16 MiB")
	}
	status := document(pageData.URL).status
	if status == 0 {
		return nil, errors.New("browser did not report the final document HTTP status")
	}
	// Error pages are returned with their status, as Firecrawl does; some sites serve full content with a 4xx.
	if status < 200 {
		return nil, fmt.Errorf("target returned HTTP %d", status)
	}
	md, err := newConverter().ConvertString(pageData.HTML, converter.WithDomain(pageData.URL))
	if err != nil {
		return nil, fmt.Errorf("convert Markdown: %w", err)
	}
	return newDocument(md, pageData.Links, pageData.Title, pageData.URL, status, "lightpanda")
}

func newConverter() *converter.Converter {
	return converter.NewConverter(converter.WithPlugins(base.NewBasePlugin(), commonmark.NewCommonmarkPlugin(), table.NewTablePlugin()))
}

func newDocument(md string, hrefs []string, title, source string, status int, engine string) (*Document, error) {
	if strings.TrimSpace(md) == "" {
		return nil, errors.New("page produced empty Markdown")
	}
	if len(md) > 2<<20 {
		return nil, errors.New("Markdown exceeds 2 MiB")
	}
	sort.Strings(hrefs)
	links := make([]string, 0, len(hrefs))
	for _, link := range hrefs {
		if len(links) == 0 || links[len(links)-1] != link {
			links = append(links, link)
		}
		if len(links) == 10000 {
			break
		}
	}
	return &Document{Markdown: md, Links: links, Metadata: Metadata{
		Title: title, SourceURL: source, StatusCode: status, FetchedAt: time.Now().UTC(), Engine: engine,
	}}, nil
}

// waitForDocument waits until the tab's current document has finished loading.
func waitForDocument(tab context.Context) error {
	for {
		var state string
		if chromedp.Run(tab, chromedp.Evaluate(`document.readyState`, &state)) == nil && state == "complete" {
			return nil
		}
		select {
		case <-tab.Done():
			return tab.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// mimeType reads Content-Type because Lightpanda leaves Response.MimeType empty.
func mimeType(resp *network.Response) string {
	value := resp.MimeType
	for name, header := range resp.Headers {
		if text, ok := header.(string); ok && strings.EqualFold(name, "content-type") {
			value = text
		}
	}
	value, _, _ = strings.Cut(value, ";")
	return strings.ToLower(strings.TrimSpace(value))
}
