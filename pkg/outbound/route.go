package outbound

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// RoutePrefix addresses an AI fallback route (ADR-990):
// /r/{route-id}/{provider path}.
const RoutePrefix = "/r/"

// RouteTargetHeader names the integration that served a route call.
const RouteTargetHeader = "X-Gregale-AI-Route-Target"

// ErrRouteNotFound reports a missing route.
var ErrRouteNotFound = errors.New("outbound AI route not found")

// AIRouteTarget is one ordered fallback step.
type AIRouteTarget struct {
	IntegrationID string
	Model         string
}

// AIRoute is an account-owned ordered list of AI integrations. A native
// route's targets share one provider; an openai route translates OpenAI chat
// completions for each target (ADR-991).
type AIRoute struct {
	ID        string
	AccountID string
	Format    string
	Targets   []AIRouteTarget
}

// RouteResolver loads AI routes; apid owns their lifecycle.
type RouteResolver interface {
	Route(ctx context.Context, routeID string) (AIRoute, error)
}

type routeIDContextKey struct{}

// withRouteID marks a route attempt so its settlement can log the route.
func withRouteID(ctx context.Context, routeID string) context.Context {
	return context.WithValue(ctx, routeIDContextKey{}, routeID)
}

func routeIDFromContext(ctx context.Context) string {
	routeID, _ := ctx.Value(routeIDContextKey{}).(string)
	return routeID
}

// routeAttemptWriter holds a target's response back while it is retryable
// and another target remains. The first non-retryable status commits it to
// the caller, and everything after streams through unchanged.
type routeAttemptWriter struct {
	dst       http.ResponseWriter
	header    http.Header
	target    string
	canFall   bool
	status    int
	rejection string
	committed bool
	discarded bool
}

func newRouteAttemptWriter(dst http.ResponseWriter, target string, canFall bool) *routeAttemptWriter {
	return &routeAttemptWriter{dst: dst, header: make(http.Header), target: target, canFall: canFall}
}

func (w *routeAttemptWriter) Header() http.Header { return w.header }

func (w *routeAttemptWriter) WriteHeader(status int) {
	if w.committed || w.discarded {
		return
	}
	w.status = status
	w.rejection = w.header.Get("X-Gregale-Outbound-Rejection")
	if w.canFall && routeFallbackStatus(status) {
		w.discarded = true
		return
	}
	for name, values := range w.header {
		w.dst.Header()[name] = values
	}
	w.dst.Header().Set(RouteTargetHeader, w.target)
	w.committed = true
	w.dst.WriteHeader(status)
}

func (w *routeAttemptWriter) Write(p []byte) (int, error) {
	if !w.committed && !w.discarded {
		w.WriteHeader(http.StatusOK)
	}
	if w.discarded {
		return len(p), nil
	}
	return w.dst.Write(p)
}

// Unwrap lets http.ResponseController reach the caller's writer for
// streaming flushes and write deadlines once the attempt is committed.
func (w *routeAttemptWriter) Unwrap() http.ResponseWriter { return w.dst }

func (w *routeAttemptWriter) Flush() {
	if !w.committed {
		return
	}
	if flusher, ok := w.dst.(http.Flusher); ok {
		flusher.Flush()
	}
}

// routeFallbackStatus covers gateway budget and availability rejections and
// provider timeouts, throttling and server errors. Client and policy errors
// (400, 403, 404) describe the request or configuration, so they return.
func routeFallbackStatus(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500
}

func parseRoutePath(escapedPath string) (string, string, bool) {
	if !strings.HasPrefix(escapedPath, RoutePrefix) {
		return "", "", false
	}
	routeID, rest, found := strings.Cut(strings.TrimPrefix(escapedPath, RoutePrefix), "/")
	if routeID == "" {
		return "", "", false
	}
	if !found {
		return routeID, "/", true
	}
	return routeID, "/" + rest, true
}

// serveRoute tries a route's targets in order through the ordinary
// integration path, so each attempt keeps its own bindings, budgets,
// retries and AI accounting. Only relay callers can use routes: their
// identity is not tied to one integration.
func (h *Handler) serveRoute(w http.ResponseWriter, r *http.Request) {
	principal, ok := relayPrincipalFromContext(r.Context())
	if !ok {
		writeProblem(w, http.StatusForbidden, "outbound_route_requires_relay", "AI routes are available only through GREGALE_OUTBOUND_URL", "")
		return
	}
	routeID, providerPath, ok := parseRoutePath(r.URL.EscapedPath())
	if !ok || h.Routes == nil || !h.aiGatewayOn() {
		writeProblem(w, http.StatusNotFound, "outbound_route_not_found", "AI route not found", "")
		return
	}
	route, err := h.Routes.Route(r.Context(), routeID)
	if errors.Is(err, ErrRouteNotFound) || (err == nil && route.AccountID != principal.accountID()) || (err == nil && len(route.Targets) == 0) {
		writeProblem(w, http.StatusNotFound, "outbound_route_not_found", "AI route not found", "")
		return
	}
	if err != nil {
		writeProblem(w, http.StatusServiceUnavailable, "outbound_route_unavailable", "AI route is temporarily unavailable", "1")
		return
	}
	body, replayable, err := bufferRouteBody(r)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "outbound_request_unreadable", "Outbound request body could not be read", "")
		return
	}
	translated := route.Format == api.OutboundAIRouteFormatOpenAI
	if translated && (!replayable || r.Method != http.MethodPost || !openAIRoutePath(providerPath)) {
		writeProblem(w, http.StatusBadRequest, "outbound_ai_translation_unsupported",
			"OpenAI-format routes accept buffered chat completions, Responses and embeddings requests only", "")
		return
	}
	targets := route.Targets
	if !replayable {
		targets = targets[:1]
	}
	ctx := withRouteID(r.Context(), route.ID)
	for index, target := range targets {
		attemptBody, attemptPath, attemptQuery := body, providerPath, r.URL.RawQuery
		var translator aiResponseTranslator
		if translated {
			translation, translateErr := h.translateRouteTarget(ctx, target, providerPath, r.URL.RawQuery, body)
			if translateErr != nil {
				h.Metrics.ObserveAITranslation("unknown", "unsupported")
				problem := api.NewProblem(http.StatusBadRequest, "outbound_ai_translation_unsupported",
					"The request cannot be translated for this route's targets", translationRefusalDetail(translateErr))
				problem.Type = "https://gregale.dev/docs/errors/outbound_ai_translation_unsupported"
				api.WriteProblem(w, problem)
				return
			}
			attemptBody, attemptPath, attemptQuery, translator = translation.body, translation.path, translation.rawQuery, translation.translator
		} else if target.Model != "" && replayable {
			if rewritten, rewriteErr := withModel(body, target.Model); rewriteErr == nil {
				attemptBody = rewritten
			}
		}
		attempt := routeAttemptRequest(r.WithContext(ctx), target.IntegrationID, attemptPath, attemptQuery, attemptBody, replayable)
		writer := newRouteAttemptWriter(w, target.IntegrationID, index < len(targets)-1)
		if translator != nil {
			translating := newTranslatingWriter(writer, translator)
			h.ServeHTTP(translating, attempt)
			translating.finish()
		} else {
			h.ServeHTTP(writer, attempt)
		}
		if !writer.discarded {
			if !writer.committed {
				writer.WriteHeader(http.StatusOK)
			}
			return
		}
		reason := "provider_status"
		if writer.rejection != "" {
			reason = "rejected"
		}
		h.Metrics.ObserveAIRouteFallback(reason)
	}
}

// openAIRoutePath reports whether an openai-format route can translate the
// provider path: chat completions (ADR-991), embeddings (ADR-995) or
// Responses API requests (ADR-996).
func openAIRoutePath(providerPath string) bool {
	return strings.HasSuffix(providerPath, "/chat/completions") || strings.HasSuffix(providerPath, "/embeddings") ||
		strings.HasSuffix(providerPath, "/responses")
}

// bufferRouteBody keeps a body of at most the AI buffer bound for replay
// across targets. A larger body streams to the first target only.
func bufferRouteBody(r *http.Request) ([]byte, bool, error) {
	if r.Body == nil || r.Body == http.NoBody {
		return nil, true, nil
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, api.OutboundAIRequestBufferMaxBytes+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(body)) > api.OutboundAIRequestBufferMaxBytes {
		r.Body = prefixedReadCloser{Reader: io.MultiReader(bytes.NewReader(body), r.Body), src: r.Body}
		return nil, false, nil
	}
	return body, true, nil
}

// translateRouteTarget rewrites an openai-format route's request for one
// target's provider. A target whose integration cannot be resolved is sent
// unchanged, so the ordinary path reports and falls back past it.
func (h *Handler) translateRouteTarget(ctx context.Context, target AIRouteTarget, providerPath, rawQuery string, body []byte) (routeTranslation, error) {
	integration, err := h.Resolver.Integration(ctx, target.IntegrationID)
	if err != nil {
		return routeTranslation{path: providerPath, rawQuery: rawQuery, body: body}, nil //nolint:nilerr // the ordinary path reports the lookup failure and the route falls back past it.
	}
	translation, err := translateRouteRequest(integration, providerPath, rawQuery, body, target.Model)
	if err != nil {
		return routeTranslation{}, err
	}
	if translation.translator != nil {
		h.Metrics.ObserveAITranslation(integration.AIProvider, "translated")
	}
	return translation, nil
}

// routeAttemptRequest addresses one target. providerPath is escaped, as the
// caller sent it or as translation built it, so a model ID such as a Bedrock
// ARN keeps its %2F instead of being escaped twice.
func routeAttemptRequest(r *http.Request, integrationID, providerPath, rawQuery string, body []byte, replayable bool) *http.Request {
	attempt := r.Clone(r.Context())
	escaped := Prefix + url.PathEscape(integrationID) + providerPath
	path, err := url.PathUnescape(escaped)
	if err != nil {
		path = escaped
	}
	attempt.URL = &url.URL{Path: path, RawQuery: rawQuery}
	if escaped != path {
		attempt.URL.RawPath = escaped
	}
	attempt.RequestURI = ""
	if !replayable {
		attempt.Body = r.Body
		return attempt
	}
	attempt.Body = io.NopCloser(bytes.NewReader(body))
	attempt.ContentLength = int64(len(body))
	if len(body) == 0 {
		attempt.Body = http.NoBody
	}
	return attempt
}
