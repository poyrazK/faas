package faas

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// ADR-20261102082606 step 3: a managed service call carries the time the
// inbound request has left, so the service proxy ends the call with it.
func TestGregaleDeadlineFollowsOnlyManagedServiceCalls(t *testing.T) {
	sent := map[string]string{}
	transport := NewGregaleReleaseTransport(roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		sent[req.URL.Hostname()] = req.Header.Get(GregaleDeadlineHeader)
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}, Request: req}, nil
	}))
	client := &http.Client{Transport: transport}
	handler := GregaleReleaseMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, url := range []string{"http://billing.svc.gregale/charge", "https://example.com/x"} {
			req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, url, nil)
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
		}
	}))
	inbound := httptest.NewRequest(http.MethodGet, "/", nil)
	inbound.Header.Set(GregaleDeadlineHeader, "5000")
	handler.ServeHTTP(httptest.NewRecorder(), inbound)

	ms, err := strconv.Atoi(sent["billing.svc.gregale"])
	if err != nil || ms <= 4000 || ms > 5000 {
		t.Fatalf("service call deadline = %q, want just under 5000", sent["billing.svc.gregale"])
	}
	if got := sent["example.com"]; got != "" {
		t.Fatalf("external call carried %s = %q", GregaleDeadlineHeader, got)
	}
}

func TestGregaleDeadlineKeepsTheShorterAndNeverZero(t *testing.T) {
	for _, tc := range []struct {
		name      string
		remaining time.Duration
		own       string
		want      string
	}{
		{name: "time left", remaining: 3 * time.Second, want: "3000"},
		{name: "caller set a shorter value", remaining: 3 * time.Second, own: "1200", want: "1200"},
		{name: "caller set a longer value", remaining: 3 * time.Second, own: "9000", want: "3000"},
		{name: "caller set an invalid value", remaining: 3 * time.Second, own: "soon", want: "3000"},
		{name: "no time left", remaining: -time.Second, want: "1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			header := http.Header{}
			if tc.own != "" {
				header.Set(GregaleDeadlineHeader, tc.own)
			}
			setGregaleDeadline(header, tc.remaining)
			if got := header.Get(GregaleDeadlineHeader); got != tc.want {
				t.Fatalf("%s = %q, want %q", GregaleDeadlineHeader, got, tc.want)
			}
		})
	}
}

func TestGregaleReleaseMiddlewareIgnoresInvalidDeadlines(t *testing.T) {
	for _, values := range [][]string{nil, {"0"}, {"-5"}, {"abc"}, {"100", "200"}, {"999999999999"}} {
		var captured bool
		handler := GregaleReleaseMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			_, captured = GregaleDeadlineFromContext(r.Context())
		}))
		inbound := httptest.NewRequest(http.MethodGet, "/", nil)
		for _, v := range values {
			inbound.Header.Add(GregaleDeadlineHeader, v)
		}
		handler.ServeHTTP(httptest.NewRecorder(), inbound)
		if captured {
			t.Fatalf("deadline captured from %v", values)
		}
	}
	if _, ok := GregaleDeadlineFromContext(WithGregaleDeadline(context.Background(), time.Time{})); ok {
		t.Fatal("zero deadline was kept")
	}
}
