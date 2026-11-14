package faas

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ADR-570: the Go helpers carry the signed chain deadline exactly as the Node
// and Python SDKs do.

const testDeadlineToken = "v1.key.root.signature"

func recordingTransport(seen map[string][]string, redirect map[string]string) http.RoundTripper {
	return NewGregaleReleaseTransport(roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		seen[req.URL.Hostname()] = req.Header.Values(GregaleRequestDeadlineHeader)
		resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}, Request: req}
		if target, ok := redirect[req.URL.Hostname()]; ok {
			resp.StatusCode = http.StatusTemporaryRedirect
			resp.Header.Set("Location", target)
		}
		return resp, nil
	}))
}

func TestGregaleRequestDeadlineWinsOnManagedCallsAndNeverEscapes(t *testing.T) {
	seen := map[string][]string{}
	client := &http.Client{Transport: recordingTransport(seen, nil)}
	ctx := WithGregaleRequestDeadline(context.Background(), testDeadlineToken)
	for _, url := range []string{"http://billing.svc.gregale/", "http://ledger.internal/", "https://example.com/"} {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		// A downstream override never replaces the request's own token.
		req.Header.Set(GregaleRequestDeadlineHeader, "v1.key.other.signature")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if got := req.Header.Get(GregaleRequestDeadlineHeader); got != "v1.key.other.signature" {
			t.Fatalf("transport mutated the caller's request header: %q", got)
		}
	}
	for _, host := range []string{"billing.svc.gregale", "ledger.internal"} {
		if got := seen[host]; len(got) != 1 || got[0] != testDeadlineToken {
			t.Fatalf("%s carried %v, want the request's token", host, got)
		}
	}
	if got := seen["example.com"]; len(got) != 0 {
		t.Fatalf("external call carried %v", got)
	}
}

func TestGregaleRequestDeadlineExplicitCarrierWithoutContext(t *testing.T) {
	for _, tc := range []struct{ explicit, want string }{
		{explicit: testDeadlineToken, want: testDeadlineToken},
		{explicit: "not a token!", want: ""},
	} {
		seen := map[string][]string{}
		client := &http.Client{Transport: recordingTransport(seen, nil)}
		req, _ := http.NewRequest(http.MethodGet, "http://billing.svc.gregale/", nil)
		req.Header.Set(GregaleRequestDeadlineHeader, tc.explicit)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if got := strings.Join(seen["billing.svc.gregale"], ","); got != tc.want {
			t.Fatalf("explicit %q: sent %q, want %q", tc.explicit, got, tc.want)
		}
	}
}

func TestGregaleRequestDeadlineIsRemovedAtAnExternalRedirect(t *testing.T) {
	seen := map[string][]string{}
	client := &http.Client{Transport: recordingTransport(seen, map[string]string{"billing.svc.gregale": "https://example.com/next"})}
	req, _ := http.NewRequestWithContext(WithGregaleRequestDeadline(context.Background(), testDeadlineToken), http.MethodGet, "http://billing.svc.gregale/", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if got := seen["billing.svc.gregale"]; len(got) != 1 || got[0] != testDeadlineToken {
		t.Fatalf("managed hop carried %v", got)
	}
	if got, ok := seen["example.com"]; !ok || len(got) != 0 {
		t.Fatalf("redirect destination reached=%v carried %v", ok, got)
	}
}

func TestGregaleReleaseMiddlewareCapturesOneValidTokenOutsideWebSockets(t *testing.T) {
	for _, tc := range []struct {
		name    string
		values  []string
		upgrade bool
		want    string
	}{
		{name: "one token", values: []string{testDeadlineToken}, want: testDeadlineToken},
		{name: "ambiguous", values: []string{testDeadlineToken, "v1.key.second.signature"}},
		{name: "malformed", values: []string{"v1 key"}},
		{name: "too long", values: []string{strings.Repeat("a", gregaleRequestDeadlineMaxLen+1)}},
		{name: "websocket upgrade", values: []string{testDeadlineToken}, upgrade: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			handler := GregaleReleaseMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				got, _ = GregaleRequestDeadlineFromContext(r.Context())
				if _, ok := GregaleDeadlineFromContext(r.Context()); ok == tc.upgrade {
					t.Errorf("time-left captured=%v on upgrade=%v", ok, tc.upgrade)
				}
			}))
			inbound := httptest.NewRequest(http.MethodGet, "/", nil)
			for _, v := range tc.values {
				inbound.Header.Add(GregaleRequestDeadlineHeader, v)
			}
			inbound.Header.Set(GregaleDeadlineHeader, "5000")
			if tc.upgrade {
				inbound.Header.Set("Connection", "Upgrade")
				inbound.Header.Set("Upgrade", "websocket")
			}
			handler.ServeHTTP(httptest.NewRecorder(), inbound)
			if got != tc.want {
				t.Fatalf("captured %q, want %q", got, tc.want)
			}
		})
	}
}
