// Package outboundai is apid's AI gateway surface: integration AI policy,
// usage and price lists, the built-in price catalog, per-end-user usage, app
// guardrails, extra provider keys, budget alerts, the request log and AI
// fallback routes (ADR-988, ADR-990, ADR-991, ADR-994). It was lifted out of
// pkg/apid/apidserver by ADR-982; the server keeps the route table and the
// ai_gateway_enabled release gate.
//
// Everything it needs from apid goes through Host, which apidserver
// implements by reading its own fields at call time.
package outboundai

import (
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// Host is what the AI gateway surface needs from apid.
type Host interface {
	httpkit.AppLoader
	Store() state.Store
	// SealCredential seals a provider credential for outboundd only. On
	// failure it has already written the problem and returns false.
	SealCredential(w http.ResponseWriter, authorization string) ([]byte, bool)
}

// Service is the AI gateway surface.
type Service struct {
	Host Host
}

func problem(w http.ResponseWriter, status int, code, detail string) {
	api.WriteProblem(w, api.NewProblem(status, code, http.StatusText(status), detail))
}

func copyInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}

func integrationPathID(w http.ResponseWriter, r *http.Request) (string, bool) {
	integrationID := r.PathValue("integration")
	if _, err := uuid.Parse(integrationID); err != nil {
		problem(w, http.StatusBadRequest, api.CodeValidation, "Integration ID must be a UUID")
		return "", false
	}
	return integrationID, true
}

// queryLimit reads ?limit=, defaulting to and capping at maximum.
func queryLimit(r *http.Request, maximum int) (int, bool) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return maximum, true
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 || limit > maximum {
		return 0, false
	}
	return limit, true
}
