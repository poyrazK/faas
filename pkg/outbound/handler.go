package outbound

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	TokenHeader = "X-Gregale-Outbound-Token"
	AppHeader   = "X-Gregale-App-ID"
	Prefix      = "/i/"

	defaultMaxBodyBytes           int64 = 25 << 20
	defaultMaxResponseBytes       int64 = 25 << 20
	defaultMaxResponseHeaderBytes int64 = 64 << 10
	defaultMaxResponseHeaders           = 128
)

var errResponseBodyTooLarge = errors.New("outbound response body exceeds gateway limit")

var hopByHopHeaders = map[string]struct{}{
	"Connection": {}, "Keep-Alive": {}, "Proxy-Authenticate": {},
	"Proxy-Authorization": {}, "TE": {}, "Trailer": {},
	"Transfer-Encoding": {}, "Upgrade": {},
}

// Handler is an explicit request-aware outbound gateway. A request to
// /i/{integrationID}/path is sent only to that integration's configured origin.
type Handler struct {
	Resolver               Resolver
	Backend                Backend
	Client                 *http.Client
	Metrics                *Metrics
	MaxBodyBytes           int64
	MaxResponseBytes       int64
	MaxResponseHeaderBytes int64
	MaxResponseHeaders     int
}

func NewHandler(resolver Resolver, backend Backend, client *http.Client) (*Handler, error) {
	if resolver == nil || backend == nil {
		return nil, errors.New("outbound resolver and backend are required")
	}
	if client == nil {
		client = &http.Client{Transport: http.DefaultTransport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	} else {
		clone := *client
		// A shared cookie jar could leak one integration's provider session to
		// another. The gateway is token/header based, so cookies are disabled.
		clone.Jar = nil
		// Redirects would turn a fixed-origin policy into an origin hop and
		// can replay a request body. The gateway owns this invariant even when
		// a caller supplies a custom HTTP client.
		clone.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		client = &clone
	}
	if client.Transport == nil {
		client.Transport = http.DefaultTransport
	}
	return &Handler{
		Resolver:               resolver,
		Backend:                backend,
		Client:                 client,
		MaxBodyBytes:           defaultMaxBodyBytes,
		MaxResponseBytes:       defaultMaxResponseBytes,
		MaxResponseHeaderBytes: defaultMaxResponseHeaderBytes,
		MaxResponseHeaders:     defaultMaxResponseHeaders,
	}, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/readyz" {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeProblem(w, http.StatusMethodNotAllowed, "outbound_method_not_allowed", "Only GET and HEAD are supported for readiness", "")
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = io.WriteString(w, "ok\n")
		}
		return
	}
	if r.Method == http.MethodConnect {
		writeProblem(w, http.StatusMethodNotAllowed, "outbound_method_not_allowed", "CONNECT is not supported", "")
		return
	}
	id, path, ok := parsePath(r.URL.Path)
	if !ok {
		writeProblem(w, http.StatusNotFound, "outbound_integration_not_found", "Outbound integration not found", "")
		return
	}
	integration, err := h.Resolver.Integration(r.Context(), id)
	if errors.Is(err, ErrIntegrationNotFound) {
		writeProblem(w, http.StatusNotFound, "outbound_integration_not_found", "Outbound integration not found", "")
		return
	}
	if errors.Is(err, ErrIntegrationDisabled) {
		writeProblem(w, http.StatusServiceUnavailable, "outbound_integration_disabled", "Outbound integration is disabled", "60")
		return
	}
	if err != nil {
		writeProblem(w, http.StatusServiceUnavailable, "outbound_integration_unavailable", "Outbound integration is unavailable", "1")
		return
	}
	if !validToken(r.Header.Get(TokenHeader), integration.TokenHash) {
		writeProblem(w, http.StatusUnauthorized, "outbound_unauthorized", "Outbound token is invalid", "")
		return
	}
	appID := r.Header.Get(AppHeader)
	if !integration.AllowsApp(appID) {
		writeProblem(w, http.StatusForbidden, "outbound_app_not_attached", "The app is not attached to this outbound integration", "")
		return
	}
	if h.MaxBodyBytes > 0 && r.ContentLength > h.MaxBodyBytes {
		writeProblem(w, http.StatusRequestEntityTooLarge, "outbound_request_too_large", "Outbound request body exceeds the gateway limit", "")
		return
	}
	if h.MaxBodyBytes > 0 {
		// ContentLength is -1 for chunked requests. Always wrap the body so
		// the same cap applies when the caller omits a length or lies about it.
		r.Body = http.MaxBytesReader(w, r.Body, h.MaxBodyBytes)
	}
	decision, err := h.Backend.Admit(r.Context(), AdmissionSpec{
		IntegrationID: integration.ID, RatePerSecond: integration.RatePerSecond,
		Burst: integration.Burst, MaxInFlight: integration.MaxInFlight,
		LeaseTTL: integration.RequestTimeout,
	})
	if err != nil {
		h.Metrics.ObserveAdmission(integration.ID, "error")
		h.Metrics.ObserveRejection(integration.ID, "backend_unavailable")
		writeProblem(w, http.StatusServiceUnavailable, "outbound_admission_unavailable", "Outbound admission is temporarily unavailable", "1")
		return
	}
	if !decision.Granted {
		h.Metrics.ObserveAdmission(integration.ID, "rejected")
		h.Metrics.ObserveRejection(integration.ID, decision.Reason)
		retry := retryAfterSeconds(decision.RetryAfter)
		w.Header().Set("X-Gregale-Outbound-Rejection", decision.Reason)
		writeProblem(w, http.StatusTooManyRequests, "outbound_budget_exhausted", "Outbound integration budget is exhausted", retry)
		return
	}
	h.Metrics.ObserveAdmission(integration.ID, "granted")
	h.Metrics.IncInFlight(integration.ID)

	ctx := r.Context()
	if integration.RequestTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, integration.RequestTimeout)
		defer cancel()
	}
	defer func() {
		h.Metrics.DecInFlight(integration.ID)
		_ = h.Backend.Release(context.WithoutCancel(ctx), integration.ID, decision.LeaseID)
	}()
	upstreamStarted := time.Now()
	upstreamURL, err := targetURL(integration.Origin, path, r.URL.RawQuery)
	if err != nil {
		h.Metrics.ObserveUpstreamError(integration.ID, time.Since(upstreamStarted))
		writeProblem(w, http.StatusBadGateway, "outbound_target_invalid", "Outbound integration target is invalid", "")
		return
	}
	upstreamReq, err := http.NewRequestWithContext(ctx, r.Method, upstreamURL, r.Body)
	if err != nil {
		h.Metrics.ObserveUpstreamError(integration.ID, time.Since(upstreamStarted))
		writeProblem(w, http.StatusBadGateway, "outbound_request_invalid", "Outbound request could not be constructed", "")
		return
	}
	upstreamReq.Header = forwardedHeaders(r.Header)
	resp, err := h.Client.Do(upstreamReq)
	if err != nil {
		h.Metrics.ObserveUpstreamError(integration.ID, time.Since(upstreamStarted))
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeProblem(w, http.StatusRequestEntityTooLarge, "outbound_request_too_large", "Outbound request body exceeds the gateway limit", "")
			return
		}
		writeProblem(w, http.StatusBadGateway, "outbound_upstream_unavailable", "Outbound provider could not be reached", "1")
		return
	}
	h.Metrics.ObserveUpstream(integration.ID, resp.StatusCode, time.Since(upstreamStarted))
	defer func() { _ = resp.Body.Close() }()
	if !responseHeadersWithinBounds(resp.Header, h.MaxResponseHeaderBytes, h.MaxResponseHeaders) {
		h.Metrics.ObserveUpstreamError(integration.ID, time.Since(upstreamStarted))
		writeProblem(w, http.StatusBadGateway, "outbound_response_headers_too_large", "Outbound provider response headers exceed the gateway limit", "")
		return
	}
	if h.MaxResponseBytes > 0 && resp.ContentLength > h.MaxResponseBytes {
		h.Metrics.ObserveUpstreamError(integration.ID, time.Since(upstreamStarted))
		writeProblem(w, http.StatusBadGateway, "outbound_response_too_large", "Outbound provider response exceeds the gateway limit", "")
		return
	}
	for k, values := range resp.Header {
		if isHopByHop(resp.Header, k) {
			continue
		}
		for _, value := range values {
			w.Header().Add(k, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	if h.MaxResponseBytes <= 0 {
		_, _ = io.Copy(w, resp.Body)
		return
	}
	capWriter := &responseBodyCapWriter{ResponseWriter: w, limit: h.MaxResponseBytes}
	if _, err := io.Copy(capWriter, resp.Body); errors.Is(err, errResponseBodyTooLarge) {
		// Headers and the upstream status are already committed, so this
		// path terminates the body at the cap instead of attempting to write
		// a second response envelope.
		h.Metrics.ObserveUpstreamError(integration.ID, time.Since(upstreamStarted))
	}
}

func responseHeadersWithinBounds(headers http.Header, maxBytes int64, maxCount int) bool {
	var totalBytes int64
	var count int
	for name, values := range headers {
		for _, value := range values {
			count++
			totalBytes += int64(len(name) + len(value))
			if (maxCount > 0 && count > maxCount) || (maxBytes > 0 && totalBytes > maxBytes) {
				return false
			}
		}
	}
	return true
}

type responseBodyCapWriter struct {
	http.ResponseWriter
	limit   int64
	written int64
}

func (w *responseBodyCapWriter) Write(p []byte) (int, error) {
	remaining := w.limit - w.written
	if remaining <= 0 {
		return 0, errResponseBodyTooLarge
	}
	if int64(len(p)) > remaining {
		n, err := w.ResponseWriter.Write(p[:remaining])
		w.written += int64(n)
		if err != nil {
			return n, err
		}
		return n, errResponseBodyTooLarge
	}
	n, err := w.ResponseWriter.Write(p)
	w.written += int64(n)
	return n, err
}

func parsePath(path string) (string, string, bool) {
	if !strings.HasPrefix(path, Prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(path, Prefix)
	id, suffix, found := strings.Cut(rest, "/")
	if !found {
		suffix = ""
	}
	if id == "" {
		return "", "", false
	}
	if suffix == "" {
		return id, "/", true
	}
	return id, "/" + suffix, true
}

func targetURL(origin *url.URL, path, rawQuery string) (string, error) {
	if origin == nil {
		return "", ErrInvalidIntegration
	}
	u := *origin
	u.Path = strings.TrimSuffix(origin.Path, "/") + "/" + strings.TrimPrefix(path, "/")
	u.RawPath = ""
	u.RawQuery = rawQuery
	u.Fragment = ""
	return u.String(), nil
}

func validToken(token string, expected [32]byte) bool {
	got := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(got[:], expected[:]) == 1
}

func forwardedHeaders(in http.Header) http.Header {
	out := make(http.Header, len(in))
	for k, values := range in {
		canonical := http.CanonicalHeaderKey(k)
		if isHopByHop(in, k) || canonical == "Host" || canonical == TokenHeader || canonical == AppHeader || strings.HasPrefix(strings.ToLower(canonical), "x-gregale-") {
			continue
		}
		out[canonical] = append([]string(nil), values...)
	}
	return out
}

func isHopByHop(headers http.Header, name string) bool {
	canonical := http.CanonicalHeaderKey(name)
	if _, ok := hopByHopHeaders[canonical]; ok {
		return true
	}
	for _, connection := range headers.Values("Connection") {
		for _, token := range strings.Split(connection, ",") {
			if http.CanonicalHeaderKey(strings.TrimSpace(token)) == canonical {
				return true
			}
		}
	}
	return false
}

func retryAfterSeconds(d time.Duration) string {
	if d <= 0 {
		return "1"
	}
	seconds := int64((d + time.Second - 1) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	return strconv.FormatInt(seconds, 10)
}

func writeProblem(w http.ResponseWriter, status int, code, title, retryAfter string) {
	p := api.NewProblem(status, code, title, title)
	p.Type = "https://gregale.dev/docs/errors/" + code
	if retryAfter != "" {
		p = p.WithHeader("Retry-After", retryAfter)
	}
	api.WriteProblem(w, p)
}

var _ http.Handler = (*Handler)(nil)
