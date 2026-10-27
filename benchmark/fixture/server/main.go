package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func main() {
	addr := flag.String("addr", "0.0.0.0:8090", "listen address")
	flag.Parse()
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ok") })
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "User-agent: *\nAllow: /\n")
	})
	mux.HandleFunc("/payload", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"marker": "PayloadComplete" + r.URL.Query().Get("id")})
	})
	mux.HandleFunc("/page/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/page/")
		if _, err := strconv.Atoi(id); err != nil {
			http.Error(w, "invalid id", 400)
			return
		}
		kind := r.URL.Query().Get("kind")
		var article strings.Builder
		fmt.Fprintf(&article, "<main><h1>HuntsmanPage%s</h1><h2>Details</h2>", id)
		for i := range 24 {
			fmt.Fprintf(&article, "<p>Paragraph %d. This document contains a complete product description, useful technical details, and a stable body for checking extraction quality.</p>", i)
		}
		article.WriteString(`<pre><code class="language-go">fmt.Println("CompleteCode")</code></pre><table><thead><tr><th>Feature</th><th>Value</th></tr></thead><tbody><tr><td>TableMarker</td><td>42</td></tr></tbody></table>`)
		for i := range 5 {
			fmt.Fprintf(&article, `<a href="/page/%d?kind=js">Page %d</a>`, i, i)
		}
		fmt.Fprintf(&article, "<p id=\"ready\">PayloadComplete%s</p></main>", id)
		html, _ := json.Marshal(article.String())
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		fmt.Fprintf(w, "<!doctype html><html><head><title>Fixture %s</title></head><body><nav>BoilerplateNavigation</nav><div id=\"app\">Loading</div>", id)
		switch kind {
		case "static":
			fmt.Fprint(w, article.String())
		case "delayed":
			fmt.Fprintf(w, `<script>fetch('/payload?id=%s').then(r=>r.json()).then(data=>{document.getElementById('app').innerHTML=%s;document.getElementById('ready').textContent=data.marker;});</script>`, id, html)
		default:
			fmt.Fprintf(w, `<script>document.getElementById('app').innerHTML=%s;</script>`, html)
		}
		fmt.Fprint(w, "</body></html>")
	})
	log.Fatal((&http.Server{Addr: *addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}).ListenAndServe())
}
