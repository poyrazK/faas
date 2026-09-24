package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httputil"
	"strings"

	"go.opentelemetry.io/otel/propagation"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/httpsec"
)

// serveUpgrade uses the standard library's HTTP/1.1 reverse proxy for the
// public-to-compute hop. Its 101 path hijacks both sides and copies bytes in
// both directions; the ordinary InternalReverseProxy body copier cannot do
// that. It also forwards a backend's non-101 rejection as ordinary HTTP.
func (p *InternalReverseProxy) serveUpgrade(w http.ResponseWriter, r *http.Request) {
	// The compute gateway's raw bridge or realtimed owns the upgraded session's
	// idle and maximum-age bounds. Detach only the ordinary request budget once
	// a valid 101 arrives; client cancellation still tears down the tunnel.
	streamCtx, detachBudget, _, cancelStream := newStreamSession(r.Context(), 0, 0)
	defer cancelStream()

	// Never pool a rejected upgrade under the logical gatewayd-internal host:
	// the database-backed dialer may select a different compute node next time.
	transport := newInternalProxyTransport(p.Dialer, p.DialTimeout)
	transport.DisableKeepAlives = true
	defer transport.CloseIdleConnections()

	proxy := &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(pr *httputil.ProxyRequest) { //nolint:contextcheck // Rewrite receives a ProxyRequest; pr.In.Context is its canonical request context.
			pr.Out.URL.Scheme = p.Target.Scheme
			pr.Out.URL.Host = p.Target.Host
			pr.Out.Host = pr.In.Host // app lookup uses the customer hostname
			clientIP, proto := p.forwardingContext(pr.In)
			if clientIP != "" {
				pr.Out.Header.Set("X-Forwarded-For", clientIP)
			}
			pr.Out.Header.Set("X-Forwarded-Proto", proto)
			propagation.TraceContext{}.Inject(pr.In.Context(), propagation.HeaderCarrier(pr.Out.Header))
		},
		ModifyResponse: func(resp *http.Response) error {
			// The outer public-edge middleware owns static policy and trace
			// headers, including for 101 (which httputil writes by hijacking).
			for name := range resp.Header {
				if httpsec.IsStaticHeader(name) || strings.EqualFold(name, api.TraceIDHeader) ||
					strings.EqualFold(name, edgeOriginalStatusHeader) {
					resp.Header.Del(name)
				}
			}
			for _, name := range []string{api.RequestIDHeader, api.ErrorCodeHeader} {
				if value := resp.Header.Get(name); value != "" {
					w.Header().Set(name, value)
					resp.Header.Del(name)
				}
			}
			if resp.StatusCode == http.StatusSwitchingProtocols {
				detachBudget()
			} else if resp.StatusCode == http.StatusGatewayTimeout &&
				strings.EqualFold(strings.TrimSpace(r.Header.Get(cloudflareWorkerHeader)), cloudflareWorkerZone) {
				w.Header().Set(edgeOriginalStatusHeader, "504")
				resp.StatusCode = edgeOrigin504TransportStatus
			}
			return nil
		},
		ErrorHandler: func(dst http.ResponseWriter, _ *http.Request, err error) {
			if errors.Is(err, context.Canceled) && r.Context().Err() != nil {
				return
			}
			if requestBudgetExpired(streamCtx) {
				p.logger().Warn("internal upgrade exceeded request budget", "target", p.Target.String(), "err", err)
				writeRequestBudgetExceededForRequest(dst, r)
				return
			}
			p.logger().Warn("internal upgrade failed", "target", p.Target.String(), "err", err)
			if errors.Is(err, ErrNoComputeCapacity) {
				writeForwarderProblem(dst, http.StatusServiceUnavailable)
				return
			}
			writeForwarderProblem(dst, http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(w, r.WithContext(streamCtx))
}
