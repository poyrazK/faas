package clientlimit

import (
	"errors"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/wire"
)

// BodyRate ends a request whose body arrives slower than minBytes per
// window, so a client cannot hold a connection by trickling its upload. The
// server's own read timeout is the whole request envelope, minutes long, so
// without this a slow body ties up a connection and a gateway slot for that
// long. Each window that brings minBytes starts the next; a window that does
// not fails the body read with a deadline error. exempt names requests that
// may idle, such as gRPC client streams. A request whose server cannot set
// read deadlines passes unchecked.
func BodyRate(next http.Handler, minBytes int64, window time.Duration, exempt func(*http.Request) bool, refused func(reason string)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body == nil || r.Body == http.NoBody || r.ContentLength == 0 || exempt(r) {
			next.ServeHTTP(w, r)
			return
		}
		b := &rateBody{ReadCloser: r.Body, rc: http.NewResponseController(w), min: minBytes, window: window, refused: refused}
		if err := b.arm(time.Now()); err != nil {
			next.ServeHTTP(w, r)
			return
		}
		r.Body = b
		next.ServeHTTP(w, r)
	})
}

// rateBody moves the connection's read deadline forward each time a window
// brings enough bytes.
type rateBody struct {
	io.ReadCloser
	rc      *http.ResponseController
	min     int64
	window  time.Duration
	refused func(reason string)

	mu   sync.Mutex
	got  int64
	once sync.Once
}

func (b *rateBody) arm(now time.Time) error {
	b.got = 0
	return b.rc.SetReadDeadline(now.Add(b.window))
}

func (b *rateBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.mu.Lock()
	b.got += int64(n)
	if b.got >= b.min && err == nil {
		_ = b.arm(time.Now())
	}
	b.mu.Unlock()
	switch {
	case errors.Is(err, os.ErrDeadlineExceeded):
		b.once.Do(func() { b.refused(ReasonBodyRate) })
	case errors.Is(err, io.EOF):
		// The body is in; the rest of the request is not a body read.
		_ = b.rc.SetReadDeadline(time.Time{})
	}
	return n, err
}

// InFlight answers 503 once max requests are already being served through
// next, so the requests a node forwards on stay bounded however many
// clients send them.
func InFlight(next http.Handler, max int, refused func(reason string)) http.Handler {
	slots := make(chan struct{}, max)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case slots <- struct{}{}:
		default:
			refused(ReasonForwardInFlight)
			w.Header().Set("Retry-After", "1")
			api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, "control_plane_busy",
				"Control plane busy", "this node is forwarding as many requests to the control plane as it allows").
				WithLimit(int64(max), int64(max)).
				WithDocs(wire.DocsBaseURL+"/errors"))
			return
		}
		defer func() { <-slots }()
		next.ServeHTTP(w, r)
	})
}
