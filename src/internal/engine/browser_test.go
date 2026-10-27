package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestBrowserIntegration(t *testing.T) {
	binary := os.Getenv("HUNTSMAN_TEST_BROWSER")
	if binary == "" {
		t.Skip("set HUNTSMAN_TEST_BROWSER to run real Lightpanda checks")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/error":
			w.WriteHeader(500)
			fmt.Fprint(w, "<h1>Target error</h1>")
		case "/never":
			fmt.Fprint(w, "<p>Loading</p>")
		case "/data":
			time.Sleep(150 * time.Millisecond)
			fmt.Fprint(w, "XhrPayload")
		case "/timer":
			fmt.Fprint(w, `<main id="m">Loading</main><script>setTimeout(() => { document.getElementById('m').innerHTML = '<p>TimerPayload</p>'; }, 400);</script>`)
		case "/xhr":
			fmt.Fprint(w, `<main id="m">Loading</main><script>const x = new XMLHttpRequest(); x.open('GET', '/data'); x.onload = () => { document.getElementById('m').innerHTML = '<p>' + x.responseText + '</p>'; }; x.send();</script>`)
		case "/stream":
			if r.URL.Query().Has("hang") {
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				return
			}
			fmt.Fprint(w, `<main id="m"></main><script>fetch('/stream?hang=1'); setTimeout(() => fetch('/data').then(r => r.text()).then(t => { m.textContent = 'Stream' + t; }), 50);</script>`)
		case "/page", "/page2/", "/page3":
			time.Sleep(time.Second)
			fmt.Fprint(w, "<p>Linked</p>")
		case "/prefetch":
			fmt.Fprint(w, `<main id="m"></main><a href="/page?n=1">1</a><a href="/page2/">2</a><a href="/page3">3</a><script>
fetch('/data').then(r => r.text()).then(t => { m.textContent = 'Prefetch' + t; });
(async () => { for (const a of document.links) { await fetch(a.href); } })();
const x = new XMLHttpRequest(); x.open('GET', '/page3?_rsc=1'); x.send();</script>`)
		case "/busy":
			fmt.Fprint(w, `<main><p>BusyPayload</p></main><script>(function poll() { fetch('/data').then(poll); })();</script>`)
		case "/jsredirect":
			fmt.Fprint(w, `<p>Redirecting</p><script>location.replace('/timer');</script>`)
		case "/forever":
			fmt.Fprint(w, `<main><p>ForeverPayload</p></main><script>setInterval(() => {}, 50); (function loop() { setTimeout(loop, 100); })();</script>`)
		case "/long":
			fmt.Fprint(w, `<main><p>LongPayload</p></main><script>setTimeout(() => {}, 30000);</script>`)
		case "/slow":
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
		case "/worker.js":
			w.Header().Set("Content-Type", "text/javascript")
			fmt.Fprint(w, `fetch('/slow');`)
		case "/beacon":
			fmt.Fprint(w, `<main><p>BeaconPayload</p></main><script>new Worker('/worker.js');</script>`)
		case "/doc.pdf":
			w.Header().Set("Content-Type", "application/pdf")
			if strings.Contains(r.UserAgent(), "Lightpanda") {
				w.(http.Flusher).Flush()
				select {
				case <-r.Context().Done():
				case <-time.After(5 * time.Second):
				}
			}
			w.Write(testPDF("PdfPayload"))
		case "/download":
			w.Header().Set("Content-Type", "application/pdf")
			w.Write(testPDF("DownloadPayload"))
		case "/fake.pdf":
			fmt.Fprint(w, `<main><p>FakePdfPayload</p></main>`)
		case "/forbidden":
			w.WriteHeader(403)
			fmt.Fprint(w, `<p>Checking your browser</p><script>(function loop() { setTimeout(loop, 10); })(); fetch('/slow');</script>`)
		case "/fingerprint":
			if strings.Contains(r.UserAgent(), "Lightpanda") {
				w.WriteHeader(403)
				fmt.Fprint(w, `<p>Access denied</p>`)
				return
			}
			fmt.Fprint(w, `<main><p>FingerprintPayload</p></main>`)
		case "/challenge":
			if strings.Contains(r.UserAgent(), "Lightpanda") {
				fmt.Fprint(w, `<h1>Access denied</h1>`)
				return
			}
			fmt.Fprint(w, `<main><p>ChallengePayload</p></main>`)
		case "/replaced":
			fmt.Fprint(w, `<main><p>ReplacedPayload</p></main><script>history.replaceState(null, '', '/elsewhere');</script>`)
		case "/cookie":
			fmt.Fprint(w, `<main><h1>Cookies</h1><p id="cookies"></p></main><script>document.querySelector('#cookies').textContent = document.cookie || 'NoCookies'; document.cookie='secret=one';</script>`)
		default:
			fmt.Fprint(w, `<title>JS test</title><nav>UnwantedNav</nav><main>Loading</main><script>setTimeout(() => {
document.querySelector('main').innerHTML = '<h1>DynamicComplete</h1><p id="ready">AsyncPayload</p><pre><code>const x = 1;</code></pre><table><tr><th>Name</th><th>Value</th></tr><tr><td>Answer</td><td>42</td></tr></table><a href="/next">Next</a>';
}, 250);</script>`)
		}
	}))
	defer server.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	browser := NewBrowser(ctx, binary, port, true, false, 100*time.Millisecond)
	defer browser.Close()
	r := request(t, server.URL)
	r.WaitSelector = "#ready"
	doc, err := browser.Render(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"# DynamicComplete", "AsyncPayload", "const x = 1;", "|", server.URL + "/next"} {
		if !strings.Contains(doc.Markdown, marker) {
			t.Fatalf("missing %q in %s", marker, doc.Markdown)
		}
	}
	if strings.Contains(doc.Markdown, "UnwantedNav") {
		t.Fatal("navigation leaked into main content")
	}
	for range 2 {
		doc, err = browser.Render(ctx, request(t, server.URL+"/cookie"))
		if err != nil || !strings.Contains(doc.Markdown, "NoCookies") {
			t.Fatalf("session isolation: %+v %v", doc, err)
		}
	}
	for _, tc := range []struct {
		path, marker string
		max          time.Duration
	}{
		{"/timer", "TimerPayload", 2 * time.Second},
		{"/xhr", "XhrPayload", 2 * time.Second},
		{"/forever", "ForeverPayload", time.Second},
		{"/stream", "StreamXhrPayload", 5 * time.Second},
		{"/jsredirect", "TimerPayload", 3 * time.Second},
		{"/busy", "BusyPayload", settleLimit + 2*time.Second},
		{"/prefetch", "PrefetchXhrPayload", 900 * time.Millisecond},
		{"/long", "LongPayload", time.Second},
		{"/beacon", "BeaconPayload", 2 * time.Second},
		{"/doc.pdf", "PdfPayload", 2 * time.Second},
		{"/download", "DownloadPayload", 2 * time.Second},
		{"/fake.pdf", "FakePdfPayload", 2 * time.Second},
		{"/replaced", "ReplacedPayload", 2 * time.Second},
	} {
		started := time.Now()
		doc, err := browser.Render(ctx, request(t, server.URL+tc.path))
		if err != nil || !strings.Contains(doc.Markdown, tc.marker) {
			t.Fatalf("%s: %+v %v", tc.path, doc, err)
		}
		if elapsed := time.Since(started); elapsed > tc.max {
			t.Fatalf("%s took %v", tc.path, elapsed)
		}
	}
	short, stop := context.WithTimeout(ctx, 700*time.Millisecond)
	r = request(t, server.URL+"/never")
	r.WaitSelector = "#missing"
	_, err = browser.Render(short, r)
	stop()
	if err == nil {
		t.Fatal("missing selector did not time out")
	}
	doc, err = browser.Render(ctx, request(t, server.URL+"/error"))
	if err != nil || doc.Metadata.StatusCode != 500 || !strings.Contains(doc.Markdown, "Target error") {
		t.Fatalf("HTTP error page not returned with its status: %+v %v", doc, err)
	}
	started := time.Now()
	doc, err = browser.Render(ctx, request(t, server.URL+"/forbidden"))
	if err != nil || doc.Metadata.StatusCode != 403 || time.Since(started) > 2*time.Second {
		t.Fatalf("HTTP 403 not returned quickly: %+v %v after %v", doc, err, time.Since(started))
	}
	for path, marker := range map[string]string{"/fingerprint": "FingerprintPayload", "/challenge": "ChallengePayload"} {
		doc, err = browser.Render(ctx, request(t, server.URL+path))
		if err != nil || doc.Metadata.StatusCode != 200 || doc.Metadata.Engine != "http" || !strings.Contains(doc.Markdown, marker) {
			t.Fatalf("%s not fetched with a Chrome fingerprint: %+v %v", path, doc, err)
		}
	}
	blocked := NewBrowser(ctx, binary, port+1, false, false, 100*time.Millisecond)
	defer blocked.Close()
	if _, err := blocked.Render(ctx, request(t, server.URL)); err == nil {
		t.Fatal("private network was not blocked")
	}
}

func TestPDFPrivateNetworkBlocked(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		w.Write(testPDF("PrivatePayload"))
	}))
	defer server.Close()
	allowed := NewBrowser(context.Background(), "", 0, true, false, 0)
	doc, err := allowed.renderPDF(context.Background(), server.URL)
	if err != nil || !strings.Contains(doc.Markdown, "PrivatePayload") || doc.Metadata.Engine != "pdf" {
		t.Fatalf("PDF not extracted: %+v %v", doc, err)
	}
	blocked := NewBrowser(context.Background(), "", 0, false, false, 0)
	if _, err := blocked.renderPDF(context.Background(), server.URL); !errors.Is(err, errPrivateAddress) {
		t.Fatalf("private PDF fetch not blocked: %v", err)
	}
}

func TestStaticFetch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.UserAgent(), "Chrome/") || r.Header.Get("Sec-Fetch-Mode") != "navigate" {
			w.WriteHeader(403)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=iso-8859-1")
		w.Write([]byte("<title>Static</title><nav>UnwantedNav</nav><main><h1>Caf\xe9</h1><div class=\"cookie\">CookieText</div><script>ScriptText</script>" +
			"<a href=\"/next#x\">Next</a><a href=\"mailto:a@b.c\">Mail</a></main><footer>FooterText</footer>"))
	}))
	defer server.Close()
	allowed := NewBrowser(context.Background(), "", 0, true, false, 0)
	doc, err := allowed.fetchStatic(context.Background(), request(t, server.URL))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc.Markdown, "# Café") || doc.Metadata.Title != "Static" || doc.Metadata.Engine != "http" ||
		len(doc.Links) != 1 || doc.Links[0] != server.URL+"/next" {
		t.Fatalf("static page not converted: %+v", doc)
	}
	for _, unwanted := range []string{"UnwantedNav", "CookieText", "ScriptText", "FooterText"} {
		if strings.Contains(doc.Markdown, unwanted) {
			t.Fatalf("%s leaked into %s", unwanted, doc.Markdown)
		}
	}
	blocked := NewBrowser(context.Background(), "", 0, false, false, 0)
	if _, err := blocked.fetchStatic(context.Background(), request(t, server.URL)); !errors.Is(err, errPrivateAddress) {
		t.Fatalf("private static fetch not blocked: %v", err)
	}
}

func testPDF(text string) []byte {
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 100] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
		"",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	stream := "BT /F1 12 Tf 10 50 Td (" + text + ") Tj ET"
	objects[3] = fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream)
	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects))
	for i, object := range objects {
		offsets[i] = out.Len()
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", i+1, object)
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, offset := range offsets {
		fmt.Fprintf(&out, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return out.Bytes()
}
