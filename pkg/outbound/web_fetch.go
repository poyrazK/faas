package outbound

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/publicdest"
	"github.com/onebox-faas/faas/pkg/safetext"
)

// WebFetch is the capability gateway's web_fetch tool (ADR-20261027090018
// tier 1, ADR-20261027090016 amendment 1): GET one URL and return its text.
// It dials only public addresses on ports 80 and 443, checked at every
// connection, follows a bounded number of redirects, and reads a bounded
// body of text types only.
type WebFetch struct {
	// On is FAAS_OUTBOUND_AGENT_WEB_FETCH. Off, the tool is never listed.
	On bool
	// Dial overrides the guarded public dialer, for tests only.
	Dial func(ctx context.Context, network, address string) (net.Conn, error)
}

// errWebFetchPort refuses a connection to a port other than 80 or 443.
var errWebFetchPort = errors.New("web_fetch reaches ports 80 and 443 only")

// webFetchDialer dials only public IPv4 addresses on web ports. The URL is
// checked first; this re-checks every connection, redirects included, and
// publicdest re-resolves the name and refuses the whole answer if any
// address is not public, so DNS cannot rebind a fetch into a private range.
func webFetchDialer() func(ctx context.Context, network, address string) (net.Conn, error) {
	public := publicdest.NewDialer()
	return func(ctx context.Context, _, address string) (net.Conn, error) {
		_, port, err := net.SplitHostPort(address)
		if err != nil || (port != "80" && port != "443") {
			return nil, errWebFetchPort
		}
		return public.DialContext(ctx, "tcp4", address)
	}
}

func (w *WebFetch) Name() string  { return api.AgentCapabilityWebFetch }
func (w *WebFetch) Enabled() bool { return w != nil && w.On }

func (w *WebFetch) Description() string {
	return fmt.Sprintf("Fetch one public web page over HTTP or HTTPS and return its text. HTML is reduced to its visible text. "+
		"Reads at most %d bytes, follows at most %d redirects, and gives up after %d seconds. Only text responses are returned.",
		api.AgentWebFetchMaxBytes, api.AgentWebFetchRedirectsMax, api.AgentWebFetchTimeoutSec)
}

func (w *WebFetch) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{"type": "string", "description": "An http or https URL on port 80 or 443"},
		},
		"required":             []string{"url"},
		"additionalProperties": false,
	}
}

// Call fetches the URL. Everything that is about the page or its address is
// a result the agent reads (isError), never a gateway fault.
func (w *WebFetch) Call(ctx context.Context, _ AgentRunIdentity, raw json.RawMessage) (string, bool, error) {
	var args struct {
		URL string `json:"url"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&args); err != nil {
		return "web_fetch: arguments must be an object with a url", true, nil
	}
	target, why := webFetchURL(args.URL)
	if why != "" {
		return "web_fetch: " + why, true, nil
	}
	ctx, cancel := context.WithTimeout(ctx, api.AgentWebFetchTimeoutSec*time.Second)
	defer cancel()
	resp, err := w.client().Do(webFetchRequest(ctx, target))
	if err != nil {
		return "web_fetch: " + webFetchFailure(err), true, nil
	}
	defer func() { _ = resp.Body.Close() }()
	return webFetchResult(resp)
}

func webFetchRequest(ctx context.Context, target *url.URL) *http.Request {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	req.Header.Set("User-Agent", "GregaleAgent/1 (+https://gregale.dev/docs/agents)")
	req.Header.Set("Accept", "text/html,text/plain,application/json,application/xml;q=0.9,*/*;q=0.1")
	return req
}

func (w *WebFetch) client() *http.Client {
	dial := w.Dial
	if dial == nil {
		dial = webFetchDialer()
	}
	return &http.Client{
		Transport: &http.Transport{DialContext: dial, Proxy: nil, ForceAttemptHTTP2: true,
			TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: api.AgentWebFetchTimeoutSec * time.Second,
			MaxResponseHeaderBytes: 64 << 10, DisableKeepAlives: true},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > api.AgentWebFetchRedirectsMax {
				return fmt.Errorf("more than %d redirects", api.AgentWebFetchRedirectsMax)
			}
			if _, why := webFetchURL(req.URL.String()); why != "" {
				return errors.New("a redirect " + why)
			}
			return nil
		},
	}
}

// webFetchURL accepts an absolute http or https URL without credentials, on
// its scheme's default port or an explicit 80 or 443.
func webFetchURL(raw string) (*url.URL, string) {
	u, err := url.Parse(strings.TrimSpace(raw))
	switch {
	case err != nil || u.Hostname() == "":
		return nil, "url must be an absolute http or https URL"
	case u.Scheme != "http" && u.Scheme != "https":
		return nil, "url must be http or https"
	case u.User != nil:
		return nil, "url must not carry credentials"
	case u.Port() != "" && u.Port() != "80" && u.Port() != "443":
		return nil, "url must use port 80 or 443"
	}
	u.Fragment = ""
	return u, ""
}

// webFetchFailure names why a fetch failed without gateway internals.
func webFetchFailure(err error) string {
	switch {
	case errors.Is(err, publicdest.ErrUnsafe):
		return "the address is not a public internet address"
	case errors.Is(err, errWebFetchPort):
		return "only ports 80 and 443 are reachable"
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Sprintf("no answer within %d seconds", api.AgentWebFetchTimeoutSec)
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) && strings.Contains(urlErr.Err.Error(), "redirect") {
		return urlErr.Err.Error()
	}
	return "the page could not be fetched"
}

// webFetchResult reads a bounded body and renders it as text.
func webFetchResult(resp *http.Response) (string, bool, error) {
	mediaType, params, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if !webFetchText(mediaType) {
		return fmt.Sprintf("web_fetch: %s answered %d with %q, which is not text", resp.Request.URL, resp.StatusCode, mediaType), true, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, api.AgentWebFetchMaxBytes+1))
	if err != nil {
		return "web_fetch: the page could not be read", true, nil //nolint:nilerr // A failed read is the page's fault: the agent reads it as an error result.
	}
	truncated := len(body) > api.AgentWebFetchMaxBytes
	if cs := strings.ToLower(params["charset"]); cs != "" && cs != "utf-8" && cs != "us-ascii" && !utf8.Valid(body) {
		return fmt.Sprintf("web_fetch: %s is in %s, which web_fetch does not decode", resp.Request.URL, cs), true, nil
	}
	// Truncate cuts on a rune boundary and replaces invalid bytes and NUL.
	text := safetext.Truncate(string(body), api.AgentWebFetchMaxBytes)
	if mediaType == "text/html" || mediaType == "application/xhtml+xml" {
		text = htmlText(text)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "URL: %s\nStatus: %d\nContent-Type: %s\n", resp.Request.URL, resp.StatusCode, mediaType)
	if truncated {
		fmt.Fprintf(&b, "Truncated: the first %d bytes\n", api.AgentWebFetchMaxBytes)
	}
	b.WriteString("\n")
	b.WriteString(text)
	return b.String(), false, nil
}

func webFetchText(mediaType string) bool {
	switch {
	case strings.HasPrefix(mediaType, "text/"):
		return true
	case mediaType == "application/json", mediaType == "application/xml", mediaType == "application/xhtml+xml",
		strings.HasSuffix(mediaType, "+json"), strings.HasSuffix(mediaType, "+xml"):
		return true
	}
	return false
}

// htmlText is a page's visible text: script, style and other non-content
// elements dropped, block elements on their own lines.
func htmlText(page string) string {
	z := html.NewTokenizer(strings.NewReader(page))
	var b strings.Builder
	skip, pre := 0, 0
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			return collapseLines(b.String())
		case html.StartTagToken, html.EndTagToken, html.SelfClosingTagToken:
			name, _ := z.TagName()
			tag := string(name)
			if htmlSkipped[tag] && tt != html.SelfClosingTagToken {
				if tt == html.StartTagToken {
					skip++
				} else if skip > 0 {
					skip--
				}
			}
			if tag == "pre" && tt == html.StartTagToken {
				pre++
			} else if tag == "pre" && tt == html.EndTagToken && pre > 0 {
				pre--
			}
			if htmlBlock[tag] {
				b.WriteString("\n")
			}
		case html.TextToken:
			switch {
			case skip > 0:
			case pre > 0:
				b.Write(z.Text())
			default:
				// Outside <pre>, a browser folds whitespace, line breaks
				// included, into single spaces.
				text := string(z.Text())
				if fields := strings.Fields(text); len(fields) > 0 {
					if strings.TrimLeft(text, " \t\r\n") != text {
						b.WriteString(" ")
					}
					b.WriteString(strings.Join(fields, " "))
					if strings.TrimRight(text, " \t\r\n") != text {
						b.WriteString(" ")
					}
				}
			}
		}
	}
}

var htmlSkipped = map[string]bool{"script": true, "style": true, "noscript": true, "template": true, "svg": true}

var htmlBlock = map[string]bool{"p": true, "div": true, "br": true, "li": true, "tr": true, "h1": true, "h2": true, "h3": true,
	"h4": true, "h5": true, "h6": true, "section": true, "article": true, "header": true, "footer": true, "pre": true,
	"blockquote": true, "table": true, "ul": true, "ol": true, "title": true}

// collapseLines trims each line and drops runs of blank lines.
func collapseLines(s string) string {
	var out []string
	blank := false
	for _, line := range strings.Split(s, "\n") {
		line = strings.Join(strings.Fields(line), " ")
		if line == "" {
			if !blank && len(out) > 0 {
				out = append(out, "")
			}
			blank = true
			continue
		}
		out = append(out, line)
		blank = false
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}
