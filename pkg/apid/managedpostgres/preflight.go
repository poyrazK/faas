// Package managedpostgres serves the managed PostgreSQL preflight surface.
package managedpostgres

import (
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	domain "github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// PreflightHandler receives the server's configured service and error/audit
// adapters. Authentication, scopes, MFA and idempotency remain route middleware.
type PreflightHandler struct {
	Service *domain.CutoverService
	Problem func(http.ResponseWriter, error)
	Audit   func(*http.Request, state.Account, string)
}

func (h PreflightHandler) Serve(w http.ResponseWriter, r *http.Request, account state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	if h.Service == nil {
		h.Problem(w, domain.ErrUnavailable)
		return
	}
	if _, err := uuid.Parse(r.PathValue("id")); err != nil {
		h.Problem(w, domain.ErrInvalid)
		return
	}
	var p domain.CutoverPreflight
	var err error
	status := http.StatusOK
	if r.Method == http.MethodPost {
		p, err = h.Service.RequestPreflight(r.Context(), account.ID, r.PathValue("id"))
		status = http.StatusAccepted
	} else {
		p, err = h.Service.Preflight(r.Context(), account.ID, r.PathValue("id"))
	}
	if err != nil {
		h.Problem(w, err)
		return
	}
	c, err := h.Service.Get(r.Context(), account.ID, p.CutoverID)
	if err != nil {
		h.Problem(w, err)
		return
	}
	if r.Method == http.MethodPost && h.Audit != nil {
		h.Audit(r, account, p.CutoverID)
	}
	httpkit.WriteJSON(w, status, PreflightView(p, c, time.Now().UTC()))
}

func PreflightView(p domain.CutoverPreflight, c domain.Cutover, now time.Time) api.ManagedPostgresCutoverPreflight {
	out := api.ManagedPostgresCutoverPreflight{CutoverID: p.CutoverID, State: p.State, Revision: p.Revision,
		Stale:      p.SourceSessionsChanged || p.RuntimeDrainChanged || p.State != "blocked" || p.CheckedAt.IsZero() || now.Before(p.CheckedAt) || !now.Before(p.ExpiresAt) || !p.CutoverUpdatedAt.Equal(c.UpdatedAt) || c.State == domain.CutoverCancelling || c.State.Closed(),
		BindingIDs: append([]string{}, p.BindingIDs...), Blockers: append([]string{}, p.Blockers...), VerificationFresh: p.VerificationFresh,
		SourceMatches: p.SourceMatches, TargetMatches: p.TargetMatches, BindingsMatch: p.BindingsMatch, CreatedAt: p.CreatedAt.UTC().Format(time.RFC3339Nano), UpdatedAt: p.UpdatedAt.UTC().Format(time.RFC3339Nano)}
	if p.SourceSessions != nil {
		source := p.SourceSessions
		view := api.ManagedPostgresCutoverSourceSessions{State: source.State, AdmissionClosed: source.AdmissionClosed,
			SelectedDatabasesDrained: source.SelectedDatabasesDrained, SelectedDatabaseCount: source.SelectedDatabaseCount,
			Sessions: source.Sessions, PreparedTransactions: source.PreparedTransactions, UnselectedDatabases: source.UnselectedDatabases,
			Blockers: append([]string{}, source.Blockers...), ObservedAt: source.ObservedAt}
		if source.WriterIsolation != nil {
			writer := api.ManagedPostgresCutoverWriterIsolation(*source.WriterIsolation)
			view.WriterIsolation = &writer
		}
		out.SourceSessions = &view
	}
	if p.RuntimeDrain != nil {
		runtime := p.RuntimeDrain
		out.RuntimeDrain = &api.ManagedPostgresCutoverRuntimeDrain{State: runtime.State, InventoryCount: runtime.InventoryCount,
			Pending: runtime.Pending, Retired: runtime.Retired, Blocked: runtime.Blocked, Blockers: append([]string{}, runtime.Blockers...), Members: []api.ManagedPostgresCutoverRuntimeDrainMember{}}
		for _, member := range runtime.Members {
			out.RuntimeDrain.Members = append(out.RuntimeDrain.Members, api.ManagedPostgresCutoverRuntimeDrainMember(member))
		}
	}
	if !p.CheckedAt.IsZero() {
		out.CheckedAt = p.CheckedAt.UTC().Format(time.RFC3339Nano)
		out.ExpiresAt = p.ExpiresAt.UTC().Format(time.RFC3339Nano)
		out.ObservedCutoverUpdatedAt = p.CutoverUpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	return out
}
