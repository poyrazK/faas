package main

import (
	"net/http"
	"runtime"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/wire"
)

// cliUserAgent names this CLI build on every API request, so server logs and
// support can tell which release sent a request and spot version skew.
func cliUserAgent() string {
	return "gregale/" + wire.Version + " (" + runtime.GOOS + "/" + runtime.GOARCH + ")"
}

// cliTransport stamps the CLI's User-Agent on API requests and relays the
// deprecation and warning headers apid answers with as one stderr line each.
type cliTransport struct {
	base http.RoundTripper
}

func wrapCLITransport(base http.RoundTripper) http.RoundTripper {
	return cliTransport{base: base}
}

func (t cliTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get("User-Agent") == "" {
		// A RoundTripper must not change the caller's request.
		req = req.Clone(req.Context())
		req.Header.Set("User-Agent", cliUserAgent())
	}
	resp, err := t.base.RoundTrip(req)
	if err == nil {
		reportServerNotice(req.Method, req.URL.Path, resp.Header)
	}
	return resp, err
}

// CloseIdleConnections keeps http.Client.CloseIdleConnections working
// through the wrapper.
func (t cliTransport) CloseIdleConnections() {
	if closer, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

var serverNotices struct {
	sync.Mutex
	seen map[string]bool
}

// reportServerNotice prints each distinct notice once per run, so a polling
// command does not repeat it on every request.
func reportServerNotice(method, path string, header http.Header) {
	message := serverNoticeMessage(method, path, header)
	if message == "" {
		return
	}
	serverNotices.Lock()
	defer serverNotices.Unlock()
	if serverNotices.seen[message] {
		return
	}
	if serverNotices.seen == nil {
		serverNotices.seen = map[string]bool{}
	}
	serverNotices.seen[message] = true
	PrintWarn(osStderr, "%s", message)
}

// serverNoticeMessage turns a response's Warning (RFC 9110), Deprecation
// (RFC 9745) and Sunset (RFC 8594) headers into one line, or "" when it has
// none. A Warning already explains itself, so it wins over the generic
// deprecation line.
func serverNoticeMessage(method, path string, header http.Header) string {
	var texts []string
	for _, value := range header.Values("Warning") {
		if text := warningText(value); text != "" {
			texts = append(texts, text)
		}
	}
	message := ""
	switch {
	case len(texts) > 0:
		message = "Gregale API: " + strings.Join(texts, "; ")
	case header.Get("Deprecation") != "":
		message = "Gregale API: " + method + " " + path + " is deprecated"
		if sunset := header.Get("Sunset"); sunset != "" {
			message += " and stops working after " + sunset
		}
		message += "; a newer gregale may use its replacement"
	default:
		return ""
	}
	return terminalSafeLine(message, 400)
}

// warningText returns the quoted warn-text of one Warning value
// (warn-code warn-agent "warn-text" ["warn-date"]).
func warningText(value string) string {
	start := strings.IndexByte(value, '"')
	if start < 0 {
		return ""
	}
	var text strings.Builder
	escaped := false
	for _, r := range value[start+1:] {
		switch {
		case escaped:
			text.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case r == '"':
			return strings.TrimSpace(text.String())
		default:
			text.WriteRune(r)
		}
	}
	return ""
}

// terminalSafeLine keeps server text on one line without control sequences
// and caps it at limit runes.
func terminalSafeLine(s string, limit int) string {
	s = strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return ' '
		}
		return r
	}, s)), " ")
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	runes := []rune(s)
	return string(runes[:limit-1]) + "…"
}
