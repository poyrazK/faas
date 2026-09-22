package main

// Per-app streaming-cap probe (ADR-102 D6).
//
// GET /v1/apps/{slug}/streaming-cap
//
// Read-only, scoped to api.ScopesReadSurface (admin or apps:read).
// No MFA required — the primary caller is an API key. IDOR-safe
// via the existing loadApp (cross-account slug → 404, not 200 with
// another tenant's streaming-cap data — leaking cap data lets a
// customer probe another tenant's plan tier).
//
// What this returns
//
// The probe is the apid-side mirror of
// pkg/gateway.(*Handler).decideStreaming. It computes the same
// status enum (api.StreamingStatus*) the gateway would stamp on
// the next inbound request, plus the same effective response body
// cap the gateway would install via capWriter.
//
// By default the probe returns the plan-level cap. Supplying all three
// request-shape query parameters (`host`, `path`, and `method`) asks the
// gatewayd control listener to resolve the matching kind=limit rule, so the
// response can report the same endpoint override the request path will use.
// The operator's FAAS_GATEWAY_STREAMING state remains gatewayd-local; the
// Streaming-Status response header on a real request is still canonical.
//
// Wire format
//
//	200  {"app_id":"...","status":"streaming",
//	      "effective_cap_bytes":104857600,
//	      "plan_cap_bytes":104857600,
//	      "flag_enabled":true,"plan_allowed":true}
//	404  problem+json                         on cross-account slug
//
// Why this lives on apid, not the gatewayd public listener: the
// auth chain (ScopesReadSurface) lives in apid where it belongs;
// the per-account rate limit applies naturally. The probe is a
// pure read against the per-app cache and gatewayd rule cache; no wake, no
// state mutation.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// isUpgradeRequest is the apid-side mirror of
// pkg/gateway.isUpgradeRequest (pkg/gateway/upgrade.go:115). Lives
// locally because importing pkg/gateway from cmd/apid would
// create a cycle (apid ↔ gatewayd is gRPC over unix socket, not
// direct linkage). The two definitions MUST stay byte-identical;
// divergence breaks the streaming-cap probe's upgrade-bypass
// classification relative to the gateway.
//
// Mirrors the gateway's full RFC 7230 §6.7 parsing: iterates every
// Connection header value (multiple Connection headers are
// allowed per RFC 7230 §3.2; net/http's Values() flattens into a
// slice), splits each on commas, trims per-spec whitespace, and
// EqualFolds each token against "Upgrade". A Connection header
// that mentions Upgrade without an actual Upgrade header is a
// malformed request and is treated as plain HTTP (returns false).
func isUpgradeRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	for _, conn := range r.Header.Values("Connection") {
		for _, tok := range strings.Split(conn, ",") {
			if strings.EqualFold(strings.TrimSpace(tok), "Upgrade") {
				return r.Header.Get("Upgrade") != ""
			}
		}
	}
	return false
}

// isAcceptJSON is the apid-side mirror of
// pkg/gateway.isAcceptJSON (pkg/gateway/handler.go:3241). Lives
// locally for the same cycle-avoidance reason as isUpgradeRequest
// above. Mirrors the gateway's "any token equals application/json"
// rule so the probe matches what the gateway would classify.
func isAcceptJSON(accept string) bool {
	if accept == "" {
		return false
	}
	for _, raw := range strings.Split(accept, ",") {
		// Strip parameters and whitespace per RFC 7231 §5.3.2.
		mediaType := strings.TrimSpace(strings.SplitN(raw, ";", 2)[0])
		if strings.EqualFold(mediaType, "application/json") {
			return true
		}
	}
	return false
}

type streamingCapUpstreamResponse struct {
	Slug                  string `json:"slug"`
	AppID                 string `json:"app_id"`
	Override              bool   `json:"override"`
	MaxBodyBytesStreaming int    `json:"max_body_bytes_streaming"`
}

// resolveStreamingCapOverride asks gatewayd for the already-compiled rule
// match. Failure is intentionally a soft fallback to the plan cap: the
// probe remains useful during a rolling restart or on a single-box install
// where the control listener is not configured.
func (s *server) resolveStreamingCapOverride(r *http.Request, slug, appID, host, requestPath, method string) (int64, bool) {
	if s.gatewaydControlURL == "" {
		return 0, false
	}
	dialCtx, cancel := context.WithTimeout(r.Context(), routesDialTimeout)
	defer cancel()
	endpoint, err := url.JoinPath(s.gatewaydControlURL, "v1", "internal", "apps", slug, "streaming-cap")
	if err != nil {
		return 0, false
	}
	query := url.Values{}
	query.Set("host", host)
	query.Set("path", requestPath)
	query.Set("method", method)
	endpoint += "?" + query.Encode()
	req, err := http.NewRequestWithContext(dialCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, false
	}
	resp, err := (&http.Client{Timeout: routesDialTimeout}).Do(req)
	if err != nil {
		if s.log != nil {
			s.log.Debug("apid to gatewayd streaming-cap dial failed", "err", err, "url", endpoint)
		}
		return 0, false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 0, false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return 0, false
	}
	var upstream streamingCapUpstreamResponse
	if err := json.Unmarshal(body, &upstream); err != nil || upstream.AppID != appID {
		return 0, false
	}
	if !upstream.Override || upstream.MaxBodyBytesStreaming <= 0 {
		return 0, false
	}
	return int64(upstream.MaxBodyBytesStreaming), true
}

// getAppStreamingCap serves GET /v1/apps/{slug}/streaming-cap.
// The auth chain matches /v1/apps/{slug}/routes (read-only, no MFA,
// primary caller is an API key with ScopesReadSurface). IDOR-safe
// via loadApp — cross-account slug is a 404.
func (s *server) getAppStreamingCap(w http.ResponseWriter, r *http.Request, acct state.Account) { //nolint:contextcheck // loadApp takes r and uses r.Context() for its DB calls.
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		// loadApp already wrote the 404.
		return
	}

	// Compute the static decision. The decision tree mirrors the gateway's
	// decideStreaming for the four non-edge-rule conjuncts.
	//
	//   1. !app.StreamingEnabled → flag-disabled
	//   2. !app.Plan.StreamingResponseAllowed() → plan-disallows
	//   3. isUpgradeRequest(r) → upgrade-bypass
	//   4. isAcceptJSON(r) → accept-json-downgrade (D3 advisory)
	//   5. otherwise → streaming
	//
	// The operator opt-in (FAAS_GATEWAY_STREAMING env) is
	// gatewayd-side state and is not part of the apid cache;
	// the probe cannot reflect it. A customer evaluating "will
	// my next request stream?" must consider the operator-side
	// flag separately. The Streaming-Status response header on
	// a real request IS the canonical signal.
	planCap := acct.Plan.MaxResponseBodyBytes()
	status := decideStaticStreamingStatus(r, app, acct.Plan)
	capKind := "plan"
	effectiveCap := planCap
	host, requestPath, method := streamingCapRequestShape(r)
	if host != "" || requestPath != "" || method != "" {
		if !validStreamingCapHost(host) || !validStreamingCapPath(requestPath) || !validStreamingCapMethod(method) {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Invalid streaming-cap request shape",
				"host, path, and method query parameters must be supplied together; path must start with '/' and method must be an uppercase token"))
			return
		}
		if streamingCapCanUseEndpointRule(status) {
			if cap, ok := s.resolveStreamingCapOverride(r, r.PathValue("slug"), app.ID, host, requestPath, method); ok {
				effectiveCap = cap
				capKind = "endpoint-rule"
			}
		}
	}
	// accept-json-downgrade is informational post-D3; the request DOES
	// stream in that case, so a route-aware probe still returns the
	// endpoint-rule cap when one matches.
	writeJSON(w, http.StatusOK, api.AppStreamingStatus{
		AppID:        app.ID,
		Status:       status,
		EffectiveCap: effectiveCap,
		PlanCap:      planCap,
		FlagEnabled:  app.StreamingEnabled,
		PlanAllowed:  acct.Plan.StreamingResponseAllowed(),
		CapKind:      capKind,
	})
}

func streamingCapRequestShape(r *http.Request) (host, requestPath, method string) {
	q := r.URL.Query()
	return strings.TrimSpace(q.Get("host")), q.Get("path"), strings.ToUpper(strings.TrimSpace(q.Get("method")))
}

func validStreamingCapMethod(method string) bool {
	if method == "" || len(method) > 32 {
		return false
	}
	for i := 0; i < len(method); i++ {
		if method[i] < 'A' || method[i] > 'Z' {
			return false
		}
	}
	return true
}

func validStreamingCapHost(host string) bool {
	return host != "" && len(host) <= 255
}

func validStreamingCapPath(requestPath string) bool {
	return requestPath != "" && len(requestPath) <= 4096 && strings.HasPrefix(requestPath, "/")
}

func streamingCapCanUseEndpointRule(status api.StreamingStatus) bool {
	return status == api.StreamingStatusStreaming || status == api.StreamingStatusAcceptJSONDowngrade
}

// decideStaticStreamingStatus is the apid-side mirror of the
// gateway's decideStreaming, scoped to the four conjuncts apid can
// evaluate without a gatewayd hop. Returns the streaming-status
// enum value the customer would see stamped on the Streaming-Status
// header for a representative request of the same shape.
//
// The Plan lives on the Account, not the App (a single account
// can have multiple apps at the same tier), so the caller threads
// it explicitly. Edge-rule lookup, operator env, and per-request
// Accept/Upgrade evaluation are gatewayd-side; this helper only
// mirrors the per-app flag + plan-tier portion of the decision.
// The full decision tree is in pkg/gateway.(*Handler).decideStreaming.
func decideStaticStreamingStatus(r *http.Request, app state.App, plan api.Plan) api.StreamingStatus {
	if !app.StreamingEnabled {
		return api.StreamingStatusFlagDisabled
	}
	if !plan.StreamingResponseAllowed() {
		return api.StreamingStatusPlanDisallows
	}
	if isUpgradeRequest(r) {
		return api.StreamingStatusUpgradeBypass
	}
	if isAcceptJSON(r.Header.Get("Accept")) {
		return api.StreamingStatusAcceptJSONDowngrade
	}
	return api.StreamingStatusStreaming
}
