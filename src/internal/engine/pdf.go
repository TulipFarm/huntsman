package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"

	"github.com/ledongthuc/pdf"
)

const maxPDFBytes = 32 << 20

var errPrivateAddress = errors.New("blocked private network address")
var errNotPDF = errors.New("response is not a PDF")

func publicDialer(allowPrivate bool) *net.Dialer {
	dialer := &net.Dialer{Timeout: 8 * time.Second}
	if !allowPrivate {
		dialer.Control = func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
				ip.IsUnspecified() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast() {
				return errPrivateAddress
			}
			return nil
		}
	}
	return dialer
}

func pdfClient(allowPrivate bool) *http.Client {
	dialer := publicDialer(allowPrivate)
	transport := &http.Transport{DialContext: dialer.DialContext, TLSHandshakeTimeout: 8 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second, MaxIdleConns: 4, IdleConnTimeout: 30 * time.Second}
	return &http.Client{Transport: transport, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("too many redirects")
		}
		return nil
	}}
}

func (b *Browser) renderPDF(ctx context.Context, target string) (*Document, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Huntsman/1.0")
	req.Header.Set("Accept", "application/pdf")
	resp, err := b.pdf.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch PDF: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return nil, fmt.Errorf("target returned HTTP %d", resp.StatusCode)
	}
	if mime := strings.ToLower(resp.Header.Get("Content-Type")); strings.Contains(mime, "html") {
		return nil, errNotPDF
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxPDFBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read PDF: %w", err)
	}
	if len(raw) > maxPDFBytes {
		return nil, errors.New("PDF exceeds 32 MiB")
	}
	if !bytes.HasPrefix(bytes.TrimLeft(raw, " \t\r\n"), []byte("%PDF-")) {
		return nil, errNotPDF
	}
	md, err := pdfMarkdown(raw)
	if err != nil {
		return nil, err
	}
	return &Document{Markdown: md, Links: []string{}, Metadata: Metadata{
		SourceURL: resp.Request.URL.String(), StatusCode: resp.StatusCode, FetchedAt: time.Now().UTC(), Engine: "pdf",
	}}, nil
}

func pdfMarkdown(raw []byte) (md string, err error) {
	// The parser panics on some malformed files.
	defer func() {
		if r := recover(); r != nil {
			md, err = "", fmt.Errorf("parse PDF: %v", r)
		}
	}()
	reader, err := pdf.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return "", fmt.Errorf("parse PDF: %w", err)
	}
	var out strings.Builder
	for i := 1; i <= reader.NumPage(); i++ {
		page := reader.Page(i)
		if page.V.IsNull() {
			continue
		}
		text, err := page.GetPlainText(nil)
		if err != nil {
			continue
		}
		if text = strings.TrimSpace(text); text != "" {
			out.WriteString(text)
			out.WriteString("\n\n")
		}
		if out.Len() > 2<<20 {
			return "", errors.New("Markdown exceeds 2 MiB")
		}
	}
	if strings.TrimSpace(out.String()) == "" {
		return "", errors.New("PDF has no extractable text")
	}
	return strings.TrimSpace(out.String()) + "\n", nil
}
