package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// EdgeRuleAsyncResolved is the compiled kind=async matcher payload. The action
// itself is empty; delivery policy comes from the existing invocation system.
type EdgeRuleAsyncResolved struct {
	ID        string
	AccountID string
	AppID     string
	Priority  int
	PathGlob  string
	Methods   map[string]bool
}

// PickFirstAsyncMatch returns the first priority-ordered async rule matching
// the public request path and method.
func PickFirstAsyncMatch(rules []EdgeRuleAsyncResolved, requestPath, method string) *EdgeRuleAsyncResolved {
	for i := range rules {
		rule := &rules[i]
		if rule.Methods != nil && !rule.Methods[method] {
			continue
		}
		if rule.PathGlob != "" {
			ok, _ := pathGlobMatch(rule.PathGlob, requestPath)
			if !ok {
				continue
			}
		}
		return rule
	}
	return nil
}

// AsyncEdgeRuleMatcher is optional so existing test and extension matchers do
// not have to grow when the async rule kind is not enabled.
type AsyncEdgeRuleMatcher interface {
	MatchAsync(ctx context.Context, host, path, method string) *EdgeRuleAsyncResolved
}

// AsyncRouteRequest is the durable work envelope emitted by the public edge.
// Headers has already had credentials, hop-by-hop, and platform-owned fields
// removed before it crosses this interface.
type AsyncRouteRequest struct {
	AppID          string
	AccountID      string
	Method         string
	Path           string
	Payload        json.RawMessage
	Headers        map[string]string
	IdempotencyKey string
}

type AsyncRouteAccepted struct {
	ID string
}

// AsyncRouteEnqueuer persists a matched request without waking the app.
type AsyncRouteEnqueuer interface {
	EnqueueAsyncRoute(context.Context, AsyncRouteRequest) (AsyncRouteAccepted, error)
}

func (h *Handler) matchAsyncRoute(r *http.Request, sidecarName string) *EdgeRuleAsyncResolved {
	if h.edgeRules == nil || sidecarName != "" || isSyntheticInvocation(r.Context()) || isUpgradeRequest(r) {
		return nil
	}
	matcher, ok := h.edgeRules.(AsyncEdgeRuleMatcher)
	if !ok {
		return nil
	}
	return matcher.MatchAsync(r.Context(), hostname(r.Host), r.URL.Path, r.Method)
}

func (h *Handler) applyEdgeRuleCacheUnlessAsync(w http.ResponseWriter, r *http.Request, app App, rec *statusRecorder, asyncRule *EdgeRuleAsyncResolved) (bool, *EdgeRuleCacheResolved) {
	if asyncRule != nil {
		return false, nil
	}
	return h.applyEdgeRuleCache(w, r, app, rec)
}

// applyEdgeRuleAsync persists the admitted request and writes the 202 response.
// It runs after authentication and both account/app rate limits, but before
// burst admission or wake, so accepted work never needs a resident VM.
func (h *Handler) applyEdgeRuleAsync(w http.ResponseWriter, r *http.Request, app App, rule *EdgeRuleAsyncResolved) bool {
	if rule == nil {
		return false
	}
	limits, ok := api.LimitsFor(app.Plan)
	if !ok || !limits.AsyncInvokeAllowed {
		api.WriteProblem(w, api.ErrPlanFeatureGated("async_route", app.Plan))
		h.observeAsyncRule(rule, "blocked", "error")
		return true
	}
	if !app.RequestInvocationsEnabled {
		api.WriteProblem(w, api.NewProblem(
			http.StatusUnprocessableEntity,
			api.CodeValidation,
			"Invocation is incompatible with this workload",
			"this app has no request invocation listener; use a queue binding for workers or the jobs API for run-to-completion work.",
		))
		h.observeAsyncRule(rule, "blocked", "error")
		return true
	}
	if h.asyncRoutes == nil {
		api.WriteProblem(w, api.ErrCapacity("async route enqueue is unavailable"))
		h.observeAsyncRule(rule, "failed", "error")
		return true
	}

	payload, problem := readAsyncRoutePayload(r, int64(limits.MaxSourceBytesPerInvocation))
	if problem != nil {
		api.WriteProblem(w, problem)
		h.observeAsyncRule(rule, "blocked", "error")
		return true
	}
	accepted, err := h.asyncRoutes.EnqueueAsyncRoute(r.Context(), AsyncRouteRequest{
		AppID:          app.ID,
		AccountID:      app.AccountID,
		Method:         r.Method,
		Path:           r.URL.RequestURI(),
		Payload:        payload,
		Headers:        asyncRouteHeaders(r.Header),
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	if err != nil || accepted.ID == "" {
		api.WriteProblem(w, api.ErrCapacity("enqueue async route"))
		h.observeAsyncRule(rule, "failed", "error")
		return true
	}

	statusURL := "/v1/invocations/" + accepted.ID
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set(api.InvocationIDHeader, accepted.ID)
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(api.AsyncInvokeResponse{ID: accepted.ID, StatusURL: statusURL})
	h.observeAsyncRule(rule, "match", "success")
	if h.edgeRuleAudit != nil {
		subject := rule.ID
		h.edgeRuleAudit.Emit(r.Context(), "edge_rule.async_enqueued", &subject, map[string]any{
			"rule_id": rule.ID, "app_id": app.ID, "invocation_id": accepted.ID,
		})
	}
	return true
}

func (h *Handler) observeAsyncRule(rule *EdgeRuleAsyncResolved, outcome, result string) {
	if h.metrics == nil {
		return
	}
	h.metrics.ObserveEdgeRuleMatch("async", outcome)
	h.metrics.ObserveEdgeRuleApply("async", result)
}

func readAsyncRoutePayload(r *http.Request, limit int64) (json.RawMessage, *api.Problem) {
	if r.ContentLength > limit {
		return nil, api.ErrPlanSourceBytes(int(limit), r.ContentLength)
	}
	if r.Body == nil || r.Body == http.NoBody || r.ContentLength == 0 {
		return json.RawMessage(`{}`), nil
	}
	reader := io.Reader(r.Body)
	var closeBody io.Closer
	if r.GetBody != nil {
		body, err := r.GetBody()
		if err != nil {
			return nil, api.ErrInternal("async route payload is unavailable")
		}
		reader = body
		closeBody = body
	}
	if closeBody != nil {
		defer func() { _ = closeBody.Close() }()
	}
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, api.ErrRequestBodyReadFailed()
	}
	if int64(len(body)) > limit {
		return nil, api.ErrPlanSourceBytes(int(limit), int64(len(body)))
	}
	if len(body) == 0 {
		body = []byte(`{}`)
	}
	if !json.Valid(body) {
		return nil, api.ErrValidation("async routes require a valid JSON request body")
	}
	return append(json.RawMessage(nil), body...), nil
}

func asyncRouteHeaders(in http.Header) map[string]string {
	out := make(map[string]string)
	connectionHeaders := make(map[string]struct{})
	for _, value := range in.Values("Connection") {
		for _, token := range strings.Split(value, ",") {
			if token = strings.ToLower(strings.TrimSpace(token)); token != "" {
				connectionHeaders[token] = struct{}{}
			}
		}
	}
	for name, values := range in {
		canonical := http.CanonicalHeaderKey(name)
		lower := strings.ToLower(canonical)
		if _, hopByHop := connectionHeaders[lower]; hopByHop {
			continue
		}
		if lower == "authorization" || lower == "cookie" || lower == "content-length" ||
			lower == "host" || strings.HasPrefix(lower, "x-faas-") || isHopByHopHeader(canonical) {
			continue
		}
		out[canonical] = strings.Join(values, ", ")
	}
	return out
}
