package reqbudget

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// ADR-093/ADR-102: admission accounting must not remove the real listener's
// flush capability or append a timeout problem after headers have been flushed.
func TestMiddlewarePreservesStreamingFlush(t *testing.T) {
	cfg := MiddlewareConfig{Default: time.Hour}
	rr := httptest.NewRecorder()
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	h := cfg.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("budget middleware hides http.Flusher")
			return
		}
		flusher.Flush()
	}))
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/events", nil).WithContext(ctx))
	if !rr.Flushed || rr.Code != http.StatusOK || rr.Body.Len() != 0 {
		t.Fatalf("flush result: flushed=%v status=%d body=%q", rr.Flushed, rr.Code, rr.Body.String())
	}
}

type controllerRecorder struct {
	*httptest.ResponseRecorder
	deadline time.Time
}

func (w *controllerRecorder) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	return nil
}

func TestMiddlewarePreservesResponseController(t *testing.T) {
	want := time.Now().Add(time.Minute)
	rr := &controllerRecorder{ResponseRecorder: httptest.NewRecorder()}
	cfg := MiddlewareConfig{Default: time.Hour}
	cfg.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if err := http.NewResponseController(w).SetWriteDeadline(want); err != nil {
			t.Errorf("set stream write deadline: %v", err)
		}
	})).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/events", nil))
	if !rr.deadline.Equal(want) {
		t.Fatalf("write deadline = %v, want %v", rr.deadline, want)
	}
}

type hijackRecorder struct {
	*httptest.ResponseRecorder
	conn net.Conn
}

func (w *hijackRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.conn, bufio.NewReadWriter(bufio.NewReader(w.conn), bufio.NewWriter(w.conn)), nil
}

func TestMiddlewarePreservesHijack(t *testing.T) {
	server, client := net.Pipe()
	defer func() { _ = server.Close() }()
	defer func() { _ = client.Close() }()
	rr := &hijackRecorder{ResponseRecorder: httptest.NewRecorder(), conn: server}
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	cfg := MiddlewareConfig{Default: time.Hour}
	cfg.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Error("budget middleware hides http.Hijacker")
			return
		}
		conn, _, err := hijacker.Hijack()
		if err != nil || conn != server {
			t.Errorf("hijack = %v, %v", conn, err)
		}
	})).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/websocket", nil).WithContext(ctx))
	if rr.Body.Len() != 0 {
		t.Fatalf("middleware wrote after hijack: %q", rr.Body.String())
	}
}

// net/http permits multiple informational responses before one final response.
// A 103 must not suppress the real status or a pre-first-byte timeout response.
func TestMiddlewarePreservesFinalStatusAfterEarlyHints(t *testing.T) {
	for _, tc := range []struct {
		name string
		want int
	}{
		{"created", http.StatusCreated},
		{"budget expired", http.StatusGatewayTimeout},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := MiddlewareConfig{Default: time.Hour}
			h := cfg.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusEarlyHints)
				w.WriteHeader(http.StatusEarlyHints)
				if tc.want == http.StatusCreated {
					w.WriteHeader(http.StatusCreated)
					_, _ = io.WriteString(w, "created")
				}
			}))
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.want == http.StatusGatewayTimeout {
					ctx, cancel := context.WithDeadline(r.Context(), time.Now().Add(-time.Second))
					defer cancel()
					r = r.WithContext(ctx)
				}
				h.ServeHTTP(w, r)
			}))
			defer srv.Close()
			client := srv.Client()
			client.Timeout = 5 * time.Second
			resp, err := client.Get(srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != tc.want {
				t.Fatalf("final status = %d, want %d", resp.StatusCode, tc.want)
			}
		})
	}
}

func TestBudgetWriterUnsupportedControllerOperations(t *testing.T) {
	// Neither a controller nor a legacy interface may silently succeed when
	// the wrapped writer lacks the requested capability.
	w := &budgetWriter{ResponseWriter: struct{ http.ResponseWriter }{httptest.NewRecorder()}}
	controller := http.NewResponseController(w)
	if err := controller.Flush(); !errors.Is(err, http.ErrNotSupported) {
		t.Fatalf("unsupported flush = %v", err)
	}
	if _, _, err := controller.Hijack(); !errors.Is(err, http.ErrNotSupported) {
		t.Fatalf("unsupported hijack = %v", err)
	}
	if w.wrote {
		t.Fatal("unsupported operation marked the response committed")
	}
}
