// adr: 1133
package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// serverRequest models what net/http hands the proxy: a declared length and
// no GetBody, so the body is read-once until the proxy buffers it.
func serverRequest(body string, contentLength int64, contentType string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "http://gateway/v1/internal/services/orders/work", io.NopCloser(strings.NewReader(body)))
	r.ContentLength = contentLength
	r.GetBody = nil
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	return r
}

func TestBufferReplayableServiceBody(t *testing.T) {
	big := strings.Repeat("x", int(api.ServiceProxyReplayBodyMaxBytes)+1)
	for _, tc := range []struct {
		name       string
		req        *http.Request
		wantOK     bool
		wantReplay bool
	}{
		{name: "small declared body", req: serverRequest("payload", 7, "application/json"), wantOK: true, wantReplay: true},
		{name: "unknown length streams", req: serverRequest("payload", -1, "application/json"), wantOK: true},
		{name: "over the cap streams", req: serverRequest(big, int64(len(big)), "application/octet-stream"), wantOK: true},
		{name: "native gRPC streams", req: serverRequest("frame", 5, "application/grpc"), wantOK: true},
		{name: "short body is a read failure", req: serverRequest("pay", 7, "application/json"), wantOK: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			ok := bufferReplayableServiceBody(rec, tc.req)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (response %d %s)", ok, tc.wantOK, rec.Code, rec.Body)
			}
			if !ok {
				if rec.Code != http.StatusBadRequest {
					t.Fatalf("status = %d, want 400 for an unreadable body", rec.Code)
				}
				return
			}
			if got := tc.req.GetBody != nil; got != tc.wantReplay {
				t.Fatalf("replayable = %v, want %v", got, tc.wantReplay)
			}
			if !tc.wantReplay {
				return
			}
			for i := 0; i < 2; i++ {
				body, err := tc.req.GetBody()
				if err != nil {
					t.Fatal(err)
				}
				got, _ := io.ReadAll(body)
				if string(got) != "payload" {
					t.Fatalf("replay %d body = %q, want payload", i, got)
				}
			}
		})
	}
}

// With small bodies buffered, a POST that lands on a just-stopped instance is
// answered by the woken target in the same request, and its body reaches the
// guest exactly once.
func TestServiceProxyReroutesUndeliveredPostWithBody(t *testing.T) {
	f := newUndeliveredFixture(t, true)
	f.park("instance-1")
	req := serverRequest("payload", 7, "application/json")
	req.Header.Set(ServiceProxyCallerAppHeader, "app-client")
	rec := httptest.NewRecorder()
	f.proxy.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `instance-woken-1 body="payload"`) {
		t.Fatalf("POST = %d %q, want 200 from the woken instance with the body", rec.Code, rec.Body.String())
	}
	if got := f.delivered.Load(); got != 1 {
		t.Fatalf("requests delivered to a guest = %d, want exactly 1", got)
	}
}
