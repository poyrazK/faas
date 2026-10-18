// spec: §4.1
package gateway

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"
)

func TestStripHopHeaders_ConnectionOptions(t *testing.T) {
	for name, strip := range map[string]func(http.Header) http.Header{
		"copy": stripHopByHop,
		"in place": func(h http.Header) http.Header {
			stripHopByHopInPlace(h)
			return h
		},
	} {
		t.Run(name, func(t *testing.T) {
			input := http.Header{
				"Connection":          {"close, x-hop-one", "\tX-Hop-Two , keep-alive"},
				"X-Hop-One":           {"remove"},
				"X-Hop-Two":           {"remove"},
				"Keep-Alive":          {"timeout=5"},
				"Proxy-Authenticate":  {"Basic realm=x"},
				"Proxy-Authorization": {"Basic xyz"},
				"Te":                  {"trailers"},
				"Trailer":             {"X-End"},
				"Trailers":            {"X-Legacy-End"},
				"Transfer-Encoding":   {"chunked"},
				"Upgrade":             {"h2c"},
				"Authorization":       {"Bearer keep"},
				"Content-Type":        {"application/json"},
				"X-Custom":            {"keep", "both"},
			}
			before := input.Clone()
			want := http.Header{
				"Authorization": {"Bearer keep"},
				"Content-Type":  {"application/json"},
				"X-Custom":      {"keep", "both"},
			}
			if got := strip(input); !reflect.DeepEqual(got, want) {
				t.Errorf("forwarded headers = %v, want %v", got, want)
			}
			if name == "copy" && !reflect.DeepEqual(input, before) {
				t.Error("header stripping mutated the original request")
			}
		})
	}
}

func TestInternalProxy_StripsConnectionOptionsBothDirections(t *testing.T) {
	seen := make(chan http.Header, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Clone()
		w.Header().Add("Connection", "X-Response-Hop-One")
		w.Header().Add("Connection", "x-response-hop-two")
		w.Header().Set("X-Response-Hop-One", "remove")
		w.Header().Set("X-Response-Hop-Two", "remove")
		w.Header().Set("X-Custom", "keep")
		_, _ = w.Write([]byte("payload"))
	}))
	t.Cleanup(upstream.Close)
	proxy := NewInternalReverseProxy(&stubDialer{server: upstream},
		&url.URL{Scheme: "http", Host: "internal"}, slog.New(slog.NewTextHandler(io.Discard, nil)), false)
	r := httptest.NewRequest(http.MethodGet, "http://app.example/", nil)
	r.Header.Add("Connection", "X-Request-Hop-One")
	r.Header.Add("Connection", "x-request-hop-two")
	r.Header.Set("X-Request-Hop-One", "remove")
	r.Header.Set("X-Request-Hop-Two", "remove")
	r.Header.Set("X-Custom", "keep")
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, r)
	if w.Code != http.StatusOK || w.Body.String() != "payload" {
		t.Fatalf("proxy response = %d %q", w.Code, w.Body.String())
	}
	for name, header := range map[string]http.Header{"request": <-seen, "response": w.Header()} {
		for _, key := range []string{"Connection", "X-Request-Hop-One", "X-Request-Hop-Two", "X-Response-Hop-One", "X-Response-Hop-Two"} {
			if header.Get(key) != "" {
				t.Errorf("%s forwarded hop-specific header %s", name, key)
			}
		}
		if header.Get("X-Custom") != "keep" {
			t.Errorf("%s lost the end-to-end header", name)
		}
	}
}
