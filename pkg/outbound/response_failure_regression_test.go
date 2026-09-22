package outbound

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type interruptedProviderBody struct {
	io.Reader
}

func (b interruptedProviderBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	if err == io.EOF {
		err = io.ErrUnexpectedEOF
	}
	return n, err
}

// A truncated unknown-length response must not look like a complete success
// to the downstream HTTP client, whether caused by the provider or our cap.
func TestHandlerAbortsIncompleteProviderResponses(t *testing.T) {
	for _, tc := range []struct {
		name      string
		limit     int64
		interrupt bool
		wantError bool
	}{
		{"provider disconnected", 100, true, true},
		{"provider disconnected without cap", 0, true, true},
		{"response cap exceeded", 4, false, true},
		{"complete response at cap", 5, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
				var body io.Reader = strings.NewReader("12345")
				if tc.interrupt {
					body = interruptedProviderBody{Reader: body}
				}
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), ContentLength: -1, Body: io.NopCloser(body), Request: r}, nil
			})
			integration := testIntegration(t, "https://provider.example", "secret", []string{"app-1"}, 100, 1, 1)
			integration.RequestTimeout = 5 * time.Second
			resolver, err := NewStaticResolver([]Integration{integration})
			if err != nil {
				t.Fatal(err)
			}
			handler, err := NewHandler(resolver, NewMemoryBackend(), &http.Client{Transport: transport})
			if err != nil {
				t.Fatal(err)
			}
			handler.MaxResponseBytes = tc.limit
			gateway := httptest.NewServer(handler)
			defer gateway.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, gateway.URL+Prefix+integration.ID+"/download", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set(TokenHeader, "secret")
			req.Header.Set(AppHeader, "app-1")
			resp, requestErr := gateway.Client().Do(req)
			var body []byte
			if requestErr == nil {
				body, requestErr = io.ReadAll(resp.Body)
				_ = resp.Body.Close()
			}
			if tc.wantError && requestErr == nil {
				t.Fatalf("incomplete provider body was reported as success: status=%d body=%q", resp.StatusCode, body)
			}
			if !tc.wantError && (requestErr != nil || string(body) != "12345") {
				t.Fatalf("complete response failed: body=%q error=%v", body, requestErr)
			}
		})
	}
}
