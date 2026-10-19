// adr: 047
// adr: 093
package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Continuous progress must keep the per-flush write window alive. Use real
// HTTP/1 and HTTP/2 servers so an expired deadline actually truncates the body.
func TestStreamingResponseWriteDeadlineSlides(t *testing.T) {
	for _, protocol := range []struct {
		name string
		h2   bool
	}{
		{name: "HTTP1"},
		{name: "HTTP2", h2: true},
	} {
		t.Run(protocol.name, func(t *testing.T) {
			const chunk = "data: progress\n\n"
			const count = 7
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				rec := &statusRecorder{ResponseWriter: w, request: r}
				stream := (&Handler{}).setupStreamingWriter(rec, rec,
					App{ID: "app", Plan: api.PlanHobby}, Target{}, 1024, 800*time.Millisecond)
				stream.Header().Set("Content-Type", "text/event-stream")
				for i := 0; i < count; i++ {
					if i > 0 {
						timer := time.NewTimer(200 * time.Millisecond)
						select {
						case <-timer.C:
						case <-r.Context().Done():
							timer.Stop()
							return
						}
					}
					if _, err := io.WriteString(stream, chunk); err != nil {
						return
					}
				}
				rec.finalFlush()
			}))
			server.EnableHTTP2 = protocol.h2
			if protocol.h2 {
				server.StartTLS()
			} else {
				server.Start()
			}
			t.Cleanup(server.Close)
			response, err := server.Client().Get(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil || string(body) != strings.Repeat(chunk, count) {
				t.Fatalf("stream truncated despite regular progress: body=%q, err=%v", body, err)
			}
			if (response.ProtoMajor == 2) != protocol.h2 {
				t.Fatalf("protocol = %s, HTTP/2 = %v", response.Proto, protocol.h2)
			}
		})
	}
}

func TestStreamingFlushWindowWithoutWriteDeadline(t *testing.T) {
	for _, deadline := range []time.Duration{0, time.Second} {
		t.Run(deadline.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				live := &countingFlushWriter{ResponseRecorder: httptest.NewRecorder()}
				rec := &statusRecorder{ResponseWriter: live}
				var flushed []int64
				rec.installFlushHook(live, func(n int64) { flushed = append(flushed, n) }, 4, time.Second, deadline)
				for _, body := range []string{"x", "y", "", "z", "uvw"} {
					if _, err := io.WriteString(rec, body); err != nil {
						t.Fatal(err)
					}
				}
				// Virtual time exercises the interval without scheduler timing.
				time.Sleep(time.Second)
				for _, body := range []string{"q", "r"} {
					if _, err := io.WriteString(rec, body); err != nil {
						t.Fatal(err)
					}
				}
				rec.finalFlush()
				if want := []int64{1, 6, 7, 8}; !slices.Equal(flushed, want) {
					t.Fatalf("flush boundaries = %v, want first chunk, byte/time windows and residual %v", flushed, want)
				}
			})
		})
	}
}
