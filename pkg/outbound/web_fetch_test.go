package outbound

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/publicdest"
)

// webFetchTo is a WebFetch whose every connection reaches srv, so tests can
// fetch named hosts without DNS.
func webFetchTo(srv *httptest.Server) *WebFetch {
	addr := strings.TrimPrefix(srv.URL, "http://")
	return &WebFetch{On: true, Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
		if _, port, _ := net.SplitHostPort(address); port != "80" && port != "443" {
			return nil, errWebFetchPort
		}
		return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	}}
}

func fetch(t *testing.T, w *WebFetch, args string) (string, bool) {
	t.Helper()
	text, isErr, err := w.Call(context.Background(), AgentRunIdentity{}, []byte(args))
	if err != nil {
		t.Fatalf("web_fetch %s: gateway fault %v", args, err)
	}
	return text, isErr
}

// adr: 20261027090018, 20261027090016 amendment 1 — web_fetch returns a
// page's visible text, refuses what is not text, bounds redirects and size,
// and reaches only web ports.
func TestWebFetch(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/page", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><head><title>Malta</title><style>p{}</style><script>steal()</script></head>
			<body><h1>Capital</h1><p>Valletta   is the
			capital.</p><br/><p>Second</p></body></html>`))
	})
	mux.HandleFunc("/data", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("/image", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte{0x89, 'P', 'N', 'G'})
	})
	mux.HandleFunc("/big", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(strings.Repeat("a", api.AgentWebFetchMaxBytes+10)))
	})
	mux.HandleFunc("/big-utf8", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		// One ASCII byte first puts the size bound inside a two-byte rune.
		_, _ = w.Write([]byte("a" + strings.Repeat("é", api.AgentWebFetchMaxBytes/2+10)))
	})
	mux.HandleFunc("/loop", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/loop", http.StatusFound) })
	mux.HandleFunc("/to-port", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.test:8080/data", http.StatusFound)
	})
	mux.HandleFunc("/hop", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/data", http.StatusMovedPermanently)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	w := webFetchTo(srv)

	text, isErr := fetch(t, w, `{"url":"http://example.test/page#frag"}`)
	if isErr || !strings.Contains(text, "Status: 200") || !strings.Contains(text, "Malta\n") ||
		!strings.Contains(text, "Capital\n") || !strings.Contains(text, "Valletta is the capital.") || !strings.Contains(text, "Second") {
		t.Fatalf("page: error=%v\n%s", isErr, text)
	}
	if strings.Contains(text, "steal") || strings.Contains(text, "p{}") || strings.Contains(text, "#frag") {
		t.Errorf("page kept script, style or the fragment:\n%s", text)
	}
	if text, isErr := fetch(t, w, `{"url":"http://example.test/hop"}`); isErr || !strings.Contains(text, `{"ok":true}`) ||
		!strings.Contains(text, "URL: http://example.test/data") {
		t.Errorf("a redirect to JSON: error=%v\n%s", isErr, text)
	}
	if text, isErr := fetch(t, w, `{"url":"http://example.test/big"}`); isErr || !strings.Contains(text, "Truncated") ||
		len(text) > api.AgentWebFetchMaxBytes+200 {
		t.Errorf("a large body: error=%v len=%d", isErr, len(text))
	}
	if text, isErr := fetch(t, w, `{"url":"http://example.test/big-utf8"}`); isErr || !strings.Contains(text, "Truncated") ||
		strings.Contains(text, "\ufffd") || !strings.HasSuffix(text, "é") || len(text) > api.AgentWebFetchMaxBytes+200 {
		t.Errorf("a large body cut inside a rune: error=%v len=%d replacement=%v", isErr, len(text), strings.Contains(text, "\ufffd"))
	}
	for args, want := range map[string]string{
		`{"url":"http://example.test/image"}`:     "not text",
		`{"url":"http://example.test/loop"}`:      "redirects",
		`{"url":"http://example.test/to-port"}`:   "port 80 or 443",
		`{"url":"ftp://example.test/x"}`:          "http or https",
		`{"url":"http://user:pw@example.test/x"}`: "credentials",
		`{"url":"http://example.test:8080/x"}`:    "port 80 or 443",
		`{"url":"/relative"}`:                     "absolute",
		`{"url":"http://example.test","x":1}`:     "arguments",
		`[]`:                                      "arguments",
	} {
		if text, isErr := fetch(t, w, args); !isErr || !strings.Contains(text, want) {
			t.Errorf("%s: error=%v %q, want a refusal naming %q", args, isErr, text, want)
		}
	}
	if (&WebFetch{}).Enabled() || !w.Enabled() {
		t.Error("Enabled must follow the switch")
	}
}

// The real dialer refuses loopback, private and non-web destinations at
// connect time.
func TestWebFetchDialerRefusesNonPublic(t *testing.T) {
	dial := webFetchDialer()
	for address, want := range map[string]error{
		"127.0.0.1:80":       publicdest.ErrUnsafe,
		"10.0.0.1:443":       publicdest.ErrUnsafe,
		"169.254.169.254:80": publicdest.ErrUnsafe,
		"93.184.215.14:22":   errWebFetchPort,
		"localhost:80":       publicdest.ErrUnsafe,
	} {
		conn, err := dial(context.Background(), "tcp", address)
		if conn != nil {
			_ = conn.Close()
		}
		if !errors.Is(err, want) {
			t.Errorf("%s: %v, want %v", address, err, want)
		}
	}
	if got := webFetchFailure(publicdest.ErrUnsafe); !strings.Contains(got, "not a public") {
		t.Errorf("failure text %q", got)
	}
}
