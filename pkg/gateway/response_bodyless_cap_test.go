// adr: 121
// spec: §4.1 — response caps apply to body bytes, not HEAD/304 metadata.
package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestHandlerResponseCapAllowsBodylessMetadata(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		name := "buffered"
		if streaming {
			name = "streaming"
		}
		t.Run(name, func(t *testing.T) {
			for _, tc := range []struct {
				name   string
				method string
				status int
			}{
				{name: "HEAD", method: http.MethodHead, status: http.StatusOK},
				{name: "not modified", method: http.MethodGet, status: http.StatusNotModified},
			} {
				t.Run(tc.name, func(t *testing.T) {
					h, backend, _ := newTestHandler(t)
					backend.setLegacyHot()
					backend.app.Plan = api.PlanHobby
					backend.app.StreamingEnabled = streaming
					h.WithStreamingEnabled(streaming)
					length := strconv.FormatInt(backend.app.Plan.MaxResponseBodyBytes()+1, 10)
					h.proxyByNode = func(Target) http.Handler {
						return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
							w.Header().Set("Content-Length", length)
							w.Header().Set("Etag", `"large-representation"`)
							w.WriteHeader(tc.status)
						})
					}
					rec := httptest.NewRecorder()
					h.ServeHTTP(rec, httptest.NewRequest(tc.method, "http://"+backend.host+"/large", nil))
					response := rec.Result()
					defer response.Body.Close()
					body, err := io.ReadAll(response.Body)
					if err != nil {
						t.Fatal(err)
					}
					if response.StatusCode != tc.status || len(body) != 0 {
						t.Fatalf("response = %d %q, want bodyless %d", response.StatusCode, body, tc.status)
					}
					if response.Header.Get("Content-Length") != length || response.Header.Get("Etag") != `"large-representation"` {
						t.Fatalf("representation metadata changed: %v", response.Header)
					}
				})
			}
		})
	}
}
