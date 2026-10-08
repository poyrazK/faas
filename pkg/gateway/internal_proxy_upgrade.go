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
// public-to-compute handshake and non-101 rejections. The successful tunnel
// owns both sockets and joins its copiers before releasing security ownership.
func (p *InternalReverseProxy) serveUpgrade(w http.ResponseWriter, r *http.Request) {
	// The compute gateway's raw bridge or realtimed owns the upgraded session's
	// idle and maximum-age bounds. Detach only the ordinary request budget once
	// a valid 101 arrives; client and security cancellation tear down the tunnel.
	streamCtx, detachBudget, _, cancelStream := newStreamSession(r.Context(), rawStreamSessionDeadline, 0)
	defer cancelStream()

	// Never pool a rejected upgrade under the logical gatewayd-internal host:
	// the database-backed dialer may select a different compute node next time.
	transport := newInternalProxyTransport(p.Dialer, p.DialTimeout)
	transport.DisableKeepAlives = true
	defer transport.CloseIdleConnections()
	stopResponse := func() {}
	defer func() {
		if stopResponse != nil {
			stopResponse()
		}
	}()

	proxy := &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(pr *httputil.ProxyRequest) { //nolint:contextcheck // Rewrite receives a ProxyRequest; pr.In.Context is its canonical request context.
			pr.Out.URL.Scheme = p.Target.Scheme
			pr.Out.URL.Host = p.Target.Host
			pr.Out.Host = pr.In.Host // app lookup uses the customer hostname
			stampTrafficStart(pr.Out)
			clientIP, proto := p.forwardingContext(pr.In)
			if clientIP != "" {
				pr.Out.Header.Set("X-Forwarded-For", clientIP)
			}
			pr.Out.Header.Set("X-Forwarded-Proto", proto)
			propagation.TraceContext{}.Inject(pr.In.Context(), propagation.HeaderCarrier(pr.Out.Header))
		},
		ModifyResponse: func(resp *http.Response) error {
			if err := bindPublicTrafficSecurity(r, resp); err != nil {
				return err
			}
			var err error
			stopResponse, err = protectUpgradeResponse(w, streamCtx, r.Context(), resp)
			if err != nil {
				return err
			}
			// The outer public-edge middleware owns static policy and trace
			// headers, including for the hijacked 101 response.
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
				return copyPublicUpgrade(streamCtx, w, r, resp)
			} else if resp.StatusCode == http.StatusGatewayTimeout &&
				strings.EqualFold(strings.TrimSpace(r.Header.Get(cloudflareWorkerHeader)), cloudflareWorkerZone) {
				w.Header().Set(edgeOriginalStatusHeader, "504")
				resp.StatusCode = edgeOrigin504TransportStatus
			}
			return nil
		},
		ErrorHandler: func(dst http.ResponseWriter, _ *http.Request, err error) {
			if errors.Is(err, errPublicUpgradeHandled) {
				return
			}
			if refusePublicTrafficSecurity(dst, r, err) {
				return
			}
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
