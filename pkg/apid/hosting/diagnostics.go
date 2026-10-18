package hosting

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	artifacts "github.com/onebox-faas/faas/pkg/hosting"
	"github.com/onebox-faas/faas/pkg/state"
)

// DiagnosticReader deliberately has no provider, mutation, account-wide usage,
// credential, or trust methods. All reads stay within one owned site.
type DiagnosticReader interface {
	GetHostingSite(context.Context, string, string) (api.HostingSite, error)
	HostingRouteSnapshot(context.Context, string) (state.HostingRouteSnapshot, error)
	GetHostingRelease(context.Context, string, string, string) (api.HostingRelease, error)
	GetHostingCIRun(context.Context, string, string, string) (api.HostingCIRun, error)
	ListHostingPreviews(context.Context, string, string) ([]api.HostingPreview, error)
}
type DiagnosticsHost interface {
	HostingDiagnosticsEnabled() bool
	HostingDiagnosticReader() DiagnosticReader
}

func (s Service) GetDiagnostics(w http.ResponseWriter, r *http.Request, account state.Account) {
	if !s.guard(w, account) {
		return
	}
	h, ok := s.Host.(DiagnosticsHost)
	if !ok || !h.HostingDiagnosticsEnabled() || h.HostingDiagnosticReader() == nil {
		api.WriteProblem(w, api.NewProblem(503, "frontend_hosting_diagnostics_disabled", "Deployment diagnostics unavailable", "internal hosting diagnostics are disabled or unconfigured"))
		return
	}
	run, valid := diagnosticRunQuery(r)
	if !valid {
		api.WriteProblem(w, api.ErrValidation("only one optional canonical run_id UUID is accepted"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.HostingDiagnosticsTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	started := time.Now().UTC()
	site, ok := s.site(w, r, account)
	if !ok {
		return
	}
	evidence, err := s.collectDiagnostics(ctx, h.HostingDiagnosticReader(), account, site, run)
	if err != nil {
		failure(w, err)
		return
	}
	// Hot disable also applies to a report whose reads were already in flight.
	if !s.guard(w, account) {
		return
	}
	if !h.HostingDiagnosticsEnabled() {
		api.WriteProblem(w, api.NewProblem(503, "frontend_hosting_diagnostics_disabled", "Deployment diagnostics unavailable", "refresh after operator review"))
		return
	}
	httpkit.WriteJSON(w, 200, artifacts.DiagnoseHosting(evidence, started, time.Now().UTC()))
}

func diagnosticRunQuery(r *http.Request) (string, bool) {
	// URL.Query silently discards malformed pairs; reject them.
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(query) > 1 {
		return "", false
	}
	values, exists := query["run_id"]
	if !exists {
		return "", len(query) == 0
	}
	if len(values) != 1 {
		return "", false
	}
	id, err := uuid.Parse(values[0])
	return values[0], err == nil && id.String() == values[0]
}

func (s Service) collectDiagnostics(ctx context.Context, reader DiagnosticReader, account state.Account, site api.HostingSite, runID string) (artifacts.DiagnosticEvidence, error) {
	e := artifacts.DiagnosticEvidence{Site: site, RunID: runID, Publication: api.HostingPublicationStatus{State: "untracked"}}
	a, ok := s.Host.(AutomationHost)
	e.AutomationAvailable = ok && a.HostingAutomationEnabled() && a.HostingAutomationStore() != nil
	p, ok := s.Host.(PreviewHost)
	e.PreviewsAvailable = ok && p.HostingPreviewsEnabled() && p.HostingPreviewStore() != nil && s.Host.HostingDurableRoutingEnabled() && site.DurableRouting
	if site.DurableRouting {
		snap, err := reader.HostingRouteSnapshot(ctx, site.ID)
		if err != nil {
			return e, err
		}
		if snap.Site.ID != site.ID || snap.Site.AccountID != account.ID || snap.Site.OriginID != s.Host.HostingOriginID() {
			return e, state.ErrNotFound
		}
		e.Site, e.Publication = snap.Site, publicationStatus(snap)
	}
	if err := collectDiagnosticRun(ctx, reader, account.ID, &e); err != nil {
		return e, err
	}
	release := e.Site.ActiveReleaseID
	if runID != "" {
		release = ""
		if e.Run != nil {
			release = e.Run.ReleaseID
		}
	}
	if release != "" {
		row, err := reader.GetHostingRelease(ctx, account.ID, site.ID, release)
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			return e, err
		}
		if err == nil {
			e.Release = &row
		}
	}
	if e.Run != nil && e.PreviewsAvailable {
		rows, err := reader.ListHostingPreviews(ctx, account.ID, site.ID)
		if err != nil {
			return e, err
		}
		for _, row := range rows {
			if row.ID == e.Run.PreviewID {
				e.Preview = &row
				break
			}
		}
		again, err := reader.GetHostingCIRun(ctx, account.ID, site.ID, runID)
		if err != nil {
			return e, err
		}
		if again != *e.Run {
			e.Changed = true
		}
	}
	return s.finishDiagnosticObservation(ctx, reader, account, e)
}

func collectDiagnosticRun(ctx context.Context, reader DiagnosticReader, account string, e *artifacts.DiagnosticEvidence) error {
	if e.RunID == "" || !e.AutomationAvailable {
		return nil
	}
	run, err := reader.GetHostingCIRun(ctx, account, e.Site.ID, e.RunID)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return err
	}
	if err == nil {
		e.Run = &run
	}
	return nil
}

func (s Service) finishDiagnosticObservation(ctx context.Context, reader DiagnosticReader, account state.Account, e artifacts.DiagnosticEvidence) (artifacts.DiagnosticEvidence, error) {
	again, err := reader.GetHostingSite(ctx, account.ID, e.Site.ID)
	if err != nil {
		return e, err
	}
	if again.ID != e.Site.ID || again.OriginID != s.Host.HostingOriginID() {
		return e, state.ErrNotFound
	}
	if again.Revision != e.Site.Revision || again.ActiveReleaseID != e.Site.ActiveReleaseID || again.Suspended != e.Site.Suspended || again.Status != e.Site.Status {
		e.Changed = true
	}
	if e.Site.DurableRouting {
		snap, err := reader.HostingRouteSnapshot(ctx, e.Site.ID)
		if err != nil {
			return e, err
		}
		if snap.Site.ID != e.Site.ID || snap.Site.AccountID != account.ID || snap.Site.OriginID != s.Host.HostingOriginID() {
			return e, state.ErrNotFound
		}
		if snap.Generation != e.Publication.Generation || snap.Site.Revision != e.Site.Revision || snap.PublishedGeneration != e.Publication.PublishedGeneration {
			e.Changed = true
		}
	}
	return e, nil
}
