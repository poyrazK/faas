package main

// /v1/internal/apps/{slug}/streaming-cap is the loopback-only resolver used
// by apid's route-aware streaming-cap probe (ADR-102 D6 follow-up).
//
// The public probe can resolve the app flag and plan, but only gatewayd has
// the compiled per-host edge-rule cache and the exact matcher used by the
// request path. Keeping this hop on the existing control listener avoids
// duplicating rule compilation in apid.

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
)

type streamingCapRuleMatcher interface {
	MatchLimit(context.Context, string, string, string) *gateway.EdgeRuleLimitResolved
}

type streamingCapResponseJSON struct {
	Slug                  string `json:"slug"`
	AppID                 string `json:"app_id"`
	Override              bool   `json:"override"`
	RuleID                string `json:"rule_id,omitempty"`
	MaxBodyBytesStreaming int    `json:"max_body_bytes_streaming,omitempty"`
}

// internalStreamingCapHandler resolves the response-side streaming cap for
// one request shape. The app lookup is deliberately supplied by run.go so
// this control listener remains free of its own database connection.
func internalStreamingCapHandler(matcher streamingCapRuleMatcher, appLookup gateway.ResolveSlugFn, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if logger != nil {
			logger.Debug("internal streaming-cap poll", "path", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			writeStreamingCapProblem(w, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is supported on this endpoint")
			return
		}

		rest := strings.TrimPrefix(r.URL.Path, "/v1/internal/apps/")
		if !strings.HasSuffix(rest, "/streaming-cap") {
			writeStreamingCapProblem(w, http.StatusNotFound, "not_found", "path must match /v1/internal/apps/<slug>/streaming-cap")
			return
		}
		slug := strings.Trim(strings.TrimSuffix(rest, "/streaming-cap"), "/")
		if slug == "" {
			writeStreamingCapProblem(w, http.StatusBadRequest, "missing_slug", "path segment slug is required")
			return
		}
		if appLookup == nil || matcher == nil {
			writeStreamingCapProblem(w, http.StatusServiceUnavailable, "lookup_unavailable", "streaming-cap resolver is not wired in this build")
			return
		}

		host := strings.TrimSpace(r.URL.Query().Get("host"))
		requestPath := r.URL.Query().Get("path")
		method := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("method")))
		if !validStreamingCapHost(host) || !validStreamingCapPath(requestPath) || !validStreamingCapMethod(method) {
			writeStreamingCapProblem(w, http.StatusBadRequest, "invalid_request_shape", "host, path, and method query parameters are required; path must start with '/' and method must be an uppercase token")
			return
		}

		appID, ok := appLookup(slug)
		if !ok || appID == "" {
			writeStreamingCapJSON(w, http.StatusOK, streamingCapResponseJSON{Slug: slug})
			return
		}
		rule := matcher.MatchLimit(r.Context(), host, requestPath, method)
		if rule == nil || rule.AppID != appID || rule.MaxBodyBytesStreaming <= 0 {
			writeStreamingCapJSON(w, http.StatusOK, streamingCapResponseJSON{Slug: slug, AppID: appID})
			return
		}
		writeStreamingCapJSON(w, http.StatusOK, streamingCapResponseJSON{
			Slug:                  slug,
			AppID:                 appID,
			Override:              true,
			RuleID:                rule.ID,
			MaxBodyBytesStreaming: rule.MaxBodyBytesStreaming,
		})
	}
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

func writeStreamingCapJSON(w http.ResponseWriter, status int, body streamingCapResponseJSON) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	data, _ := json.Marshal(body)
	_, _ = w.Write(data)
}

func writeStreamingCapProblem(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	api.WriteProblem(w, api.NewProblem(status, code, "Streaming cap read failed", detail))
}
