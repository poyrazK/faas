// adr: 375
package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reqbudget"
)

const (
	trafficResponseDeadlineHeader = "X-Faas-Traffic-Response-Deadline"
	trafficResponseSessionHeader  = "X-Faas-Traffic-Response-Session"
)

// These controls belong to the protected compute transport, including when a
// guest attempts to smuggle one through a declared or undeclared trailer.
func isTrafficResponseControlHeader(name string) bool {
	name = strings.TrimSpace(name)
	if len(name) >= len(http.TrailerPrefix) && strings.EqualFold(name[:len(http.TrailerPrefix)], http.TrailerPrefix) {
		name = strings.TrimSpace(name[len(http.TrailerPrefix):])
	}
	return strings.EqualFold(name, trafficResponseDeadlineHeader) ||
		strings.EqualFold(name, trafficResponseSessionHeader) ||
		strings.EqualFold(name, api.StreamingStatusHeader)
}

// armResponseWriteContext interrupts both HTTP/1 socket writes and HTTP/2
// flow-control waits. Stopping and joining the callback prevents a late cancel
// from changing the deadline of the next request on a reused connection.
// Do not clear the deadline: net/http must still bound its final buffered flush.
func armResponseWriteContext(ctx context.Context, w http.ResponseWriter) (func(), error) {
	controller := http.NewResponseController(w)
	if deadline, ok := ctx.Deadline(); ok {
		if err := controller.SetWriteDeadline(deadline); err != nil {
			if errors.Is(err, http.ErrNotSupported) {
				return func() {}, nil // in-memory/non-network writers
			}
			return nil, fmt.Errorf("install response write deadline: %w", err)
		}
	}
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(done)
		_ = controller.SetWriteDeadline(time.Now())
	})
	return func() {
		if !stop() {
			<-done
		}
	}, nil
}

func guardResponseWrites(ctx context.Context, w http.ResponseWriter) func() {
	stop, err := armResponseWriteContext(ctx, w)
	if err != nil {
		panic(http.ErrAbortHandler)
	}
	return stop
}

// commitTrafficResponse is called after guest/edge header mutations. The
// closures read the handler's final, rebound request only on its own goroutine.
func (s *statusRecorder) commitTrafficResponse(code int) {
	for name := range s.Header() {
		if isTrafficResponseControlHeader(name) {
			s.Header().Del(name)
		}
	}
	if s.trafficStreamingStatus != "" {
		s.Header().Set(api.StreamingStatusHeader, string(s.trafficStreamingStatus))
	}
	if s.trafficResponseContext == nil {
		return
	}
	ctx := s.trafficResponseContext()
	if s.trafficResponseLongLived != nil && s.trafficResponseLongLived(code) {
		s.Header().Set(trafficResponseSessionHeader, "long-lived")
		lifetime, detach, cancel := reqbudget.WithStream(ctx)
		detach()
		s.trafficResponseCancel = cancel
		s.trafficResponseStop = guardResponseWrites(lifetime, s.ResponseWriter)
		return
	}
	// A 504 generated after expiry is best effort, with a separate bounded
	// error-write allowance. Never grant this allowance to a successful body.
	if (code == http.StatusGatewayTimeout || (code >= http.StatusBadRequest && trafficRevocationCause(ctx) != nil)) && ctx.Err() != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), api.RequestBudgetErrorWriteTimeout)
		s.trafficResponseCancel = cancel
	}
	if deadline, ok := ctx.Deadline(); ok {
		s.Header().Set(trafficResponseDeadlineHeader, strconv.FormatInt(deadline.UnixNano(), 10))
	}
	s.trafficResponseStop = guardResponseWrites(ctx, s.ResponseWriter)
}

func (s *statusRecorder) stopTrafficResponse() {
	if s.trafficResponseStop != nil {
		s.trafficResponseStop()
	}
	if s.trafficResponseCancel != nil {
		s.trafficResponseCancel()
	}
}

// responseBodyContext consumes only trusted compute metadata. It cannot extend
// the public listener's deadline, and no application Content-Type can detach it.
func responseBodyContext(parent, public context.Context, resp *http.Response) (context.Context, context.CancelFunc, error) {
	values := resp.Header.Values(trafficResponseDeadlineHeader)
	resp.Header.Del(trafficResponseDeadlineHeader)
	if len(values) > 1 {
		return nil, nil, errors.New("ambiguous compute response deadline")
	}
	deadline, hasDeadline := public.Deadline()
	if len(values) == 1 {
		ns, err := strconv.ParseInt(values[0], 10, 64)
		if err != nil || ns <= 0 {
			return nil, nil, errors.New("invalid compute response deadline")
		}
		if candidate := time.Unix(0, ns); !hasDeadline || candidate.Before(deadline) {
			deadline, hasDeadline = candidate, true
		}
	}
	if hasDeadline {
		ctx, cancel := context.WithDeadline(parent, deadline)
		return ctx, cancel, nil
	}
	ctx, cancel := context.WithCancel(parent)
	return ctx, cancel, nil
}

// Rejected upgrades are ordinary responses copied by httputil.ReverseProxy.
// Bind both that copier's read and its write to the compute deadline too.
func protectUpgradeResponse(w http.ResponseWriter, parent, public context.Context, resp *http.Response) (func(), error) {
	resp.Header.Del(trafficResponseSessionHeader)
	stripTrafficControlTrailerDeclarations(resp.Header)
	stripTrafficControlTrailers(resp.Trailer)
	if resp.StatusCode == http.StatusSwitchingProtocols {
		resp.Header.Del(trafficResponseDeadlineHeader)
		return func() {}, nil
	}
	ctx, cancel, err := responseBodyContext(parent, public, resp)
	if err != nil {
		return nil, err
	}
	stopWrite, err := armResponseWriteContext(ctx, w)
	if err != nil {
		cancel()
		return nil, err
	}
	reader, stopRead := newCtxReader(ctx, resp.Body)
	resp.Body = &contextResponseBody{Reader: reader, Closer: resp.Body, response: resp}
	return func() {
		stopRead()
		stopWrite()
		cancel()
	}, nil
}

type contextResponseBody struct {
	io.Reader
	io.Closer
	response *http.Response
}

func (b *contextResponseBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	if err != nil {
		stripTrafficControlTrailers(b.response.Trailer)
	}
	return n, err
}

func stripTrafficControlTrailers(header http.Header) {
	for name := range header {
		if isTrafficResponseControlHeader(name) {
			header.Del(name)
		}
	}
}

func stripTrafficControlTrailerDeclarations(header http.Header) {
	values := header.Values("Trailer")
	header.Del("Trailer")
	for _, value := range values {
		for _, name := range strings.Split(value, ",") {
			name = strings.TrimSpace(name)
			if name != "" && !isTrafficResponseControlHeader(name) {
				header.Add("Trailer", name)
			}
		}
	}
}
