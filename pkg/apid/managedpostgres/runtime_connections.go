package managedpostgres

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	domain "github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

type RuntimeConnectionHandler struct {
	Service  RuntimeConnectionRecorder
	Verifier *workloadidentity.Verifier
	Problem  func(http.ResponseWriter, error)
}

type RuntimeConnectionRecorder interface {
	RecordRuntimeConnections(context.Context, domain.RuntimeConnectionAuthority, domain.RuntimeConnectionReport) error
}

// Report accepts only workload identity, never an owner API key or app JWT.
// The catalog adapter atomically checks live instance and binding authority.
func (h RuntimeConnectionHandler) Report(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	claims, err := h.Verifier.Verify(runtimeConnectionBearer(r), workloadidentity.DataAPIConnectionsAudience, time.Now())
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized, "Workload identity required", "a Data API connection-report workload token is required"))
		return
	}
	if h.Service == nil {
		h.Problem(w, domain.ErrUnavailable)
		return
	}
	report, err := decodeRuntimeConnectionReport(w, r)
	if err != nil {
		h.Problem(w, domain.ErrInvalid)
		return
	}
	authority := domain.RuntimeConnectionAuthority{AccountID: claims.AccountID, AppID: claims.AppID, InstanceID: claims.InstanceID, TokenExpiresAt: claims.Expiry.Time()}
	if err := h.Service.RecordRuntimeConnections(r.Context(), authority, report); err != nil {
		h.Problem(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func decodeRuntimeConnectionReport(w http.ResponseWriter, r *http.Request) (domain.RuntimeConnectionReport, error) {
	var report domain.RuntimeConnectionReport
	var fields map[string]json.RawMessage
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, api.DataAPIRuntimeConnectionReportMaxBytes))
	if decoder.Decode(&fields) != nil || !errors.Is(decoder.Decode(new(any)), io.EOF) || len(fields) != 11 {
		return report, domain.ErrInvalid
	}
	for _, key := range []string{"version", "binding_id", "database_identity", "database_generation", "credential_generation", "role", "state", "runtime_model", "engine_connections", "refresh_connections", "observed_at"} {
		if value, ok := fields[key]; !ok || string(value) == "null" {
			return report, domain.ErrInvalid
		}
	}
	data, err := json.Marshal(fields)
	if err != nil || json.Unmarshal(data, &report) != nil {
		return report, domain.ErrInvalid
	}
	return report, nil
}

func runtimeConnectionBearer(r *http.Request) string {
	if len(r.Header.Values("Authorization")) != 1 || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
}
