// adr:1574 — guided production cutover over existing domain owners.
package apidserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/migrationcutover"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) cutoverBackend(w http.ResponseWriter, r *http.Request, acct state.Account) (migrationcutover.Backend, bool) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return migrationcutover.Backend{}, false
	}
	store, ok := s.store.(state.MigrationCutoverStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("saved cutover plans are unavailable"))
		return migrationcutover.Backend{}, false
	}
	b, ok := s.cutoverBackendForApp(acct, app, store)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("migration verification receipts are unavailable"))
	}
	return b, ok
}
func (s *server) createMigrationCutover(w http.ResponseWriter, r *http.Request, acct state.Account) {
	b, ok := s.cutoverBackend(w, r, acct)
	if !ok {
		return
	}
	var in api.CreateMigrationCutoverRequest
	if err := decodeJSONSized(r, &in, api.MigrationCutoverRequestMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid cutover plan"))
		return
	}
	out, err := b.Create(r.Context(), in)
	s.respondCutover(w, out, err)
}
func (s *server) getMigrationCutover(w http.ResponseWriter, r *http.Request, acct state.Account) {
	b, ok := s.cutoverBackend(w, r, acct)
	if !ok {
		return
	}
	out, err := b.Get(r.Context(), r.PathValue("plan"))
	s.respondCutover(w, out, err)
}
func (s *server) listMigrationCutovers(w http.ResponseWriter, r *http.Request, acct state.Account) {
	b, ok := s.cutoverBackend(w, r, acct)
	if !ok {
		return
	}
	out, err := b.List(r.Context())
	s.respondCutover(w, out, err)
}
func (s *server) actOnMigrationCutover(w http.ResponseWriter, r *http.Request, acct state.Account) {
	b, ok := s.cutoverBackend(w, r, acct)
	if !ok {
		return
	}
	var in api.MigrationCutoverActionRequest
	if err := decodeJSONSized(r, &in, api.MigrationCutoverRequestMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid cutover action"))
		return
	}
	out, err := b.Act(r.Context(), r.PathValue("plan"), in)
	s.respondCutover(w, out, err)
}
func (s *server) respondCutover(w http.ResponseWriter, out any, err error) {
	if err != nil {
		var problem *api.Problem
		switch {
		case errors.As(err, &problem):
		case errors.Is(err, state.ErrNotFound):
			problem = api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Plan unavailable", "No such cutover plan for this app.")
		case errors.Is(err, state.ErrConflict):
			problem = api.NewProblem(http.StatusConflict, api.CodeConflict, "Plan changed", "Reload the saved plan and review the current hostname and deployment before continuing. Up to 20 plans are retained per app.")
		default:
			problem = api.ErrCapacity("The cutover plan could not refresh. Saved DNS recovery instructions remain available when service returns.")
		}
		api.WriteProblem(w, problem)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

func (s *server) cutoverBackendForApp(acct state.Account, app state.App, store state.MigrationCutoverStore) (migrationcutover.Backend, bool) {
	verifications, ok := s.store.(state.MigrationVerificationStore)
	if !ok {
		return migrationcutover.Backend{}, false
	}
	v := s.migrationBackendForApp(acct, app, verifications)
	return migrationcutover.Backend{Store: store, AccountID: acct.ID, AppID: app.ID, App: app.Slug, Verification: v.Get, Domain: s.store.DomainByName, Instructions: customDomainDNSRecords,
		Probe: func(ctx context.Context, plan api.MigrationCutover) ([]api.MigrationVerificationCheck, error) {
			return s.probeCutover(ctx, app, plan)
		},
		Traffic: func(ctx context.Context, plan api.MigrationCutover) api.MigrationCutoverTraffic {
			unknown := api.MigrationCutoverTraffic{Status: "unknown", Coverage: "deployment_route_observed_only", Reason: "traffic_evidence_unavailable"}
			monitor, ok := s.store.(state.RouteMonitorStore)
			if !ok {
				return unknown
			}
			budget := plan.Request.Max5xxRateBPS
			preview, err := monitor.PreviewRouteMonitor(ctx, acct.ID, app.ID, api.PreviewRouteMonitorRequest{Routes: []api.RouteMonitorRoute{{Method: "GET", Path: plan.SmokePath, Max5xxRateBPS: &budget, MaxP95MS: plan.Request.MaxP95MS}}})
			if err != nil {
				return unknown
			}
			return migrationcutover.Traffic(preview, plan, app.ID)
		},
	}, true
}
func (s *server) probeCutover(ctx context.Context, app state.App, plan api.MigrationCutover) ([]api.MigrationVerificationCheck, error) {
	probeCtx, cancel := context.WithTimeout(ctx, api.MigrationCutoverProbeTimeout)
	defer cancel()
	d, err := s.store.DomainByName(probeCtx, plan.Request.Hostname)
	if err != nil {
		return nil, err
	}
	checks := []api.MigrationVerificationCheck{{Name: "ownership", Status: "pending", Detail: "Publish the TXT ownership record and wait for Gregale domain verification."}, {Name: "dns_routing", Status: "pending", Detail: "Publish the Gregale routing record. DNS must point directly to Gregale; turn off any CDN proxy for this hostname."}}
	if d.AppID != app.ID || d.EnvironmentID != "" {
		return checks, nil
	}
	if d.Verified() {
		checks[0].Status, checks[0].Detail = "passed", "Gregale verified ownership of this domain binding."
	}
	if checkPointsToGregale(probeCtx, d.Domain).Status == probeOK {
		checks[1].Status, checks[1].Detail = "passed", "Current DNS routing points to Gregale."
	}
	if checks[0].Status != "passed" || checks[1].Status != "passed" {
		return checks, nil
	}
	allowed, err := migrationcutover.EdgeAddressPolicy(probeCtx, customDomainTarget(), customDomainAddresses(), certLookupIPFunc, isPublicCertAddress)
	if err != nil {
		return append(checks, api.MigrationVerificationCheck{Name: "deployment_route", Status: "pending", Detail: "Gregale's published public edge addresses could not be confirmed. Retry domain diagnostics before continuing."}), nil
	}
	client := migrationcutover.PublicClient(d.Domain, certLookupIPFunc, allowed, certDialContextFunc)
	routeChecks := migrationcutover.ProbeRoute(probeCtx, plan, client, func(ctx context.Context, dep, token string, expires time.Time) error {
		payload, _ := json.Marshal(struct {
			AppID        string    `json:"app_id"`
			DeploymentID string    `json:"deployment_id"`
			Token        string    `json:"token"`
			ExpiresAt    time.Time `json:"expires_at"`
		}{app.ID, dep, token, expires})
		return s.notif.Notify(ctx, db.NotifyDeploymentSmokeChallenge, string(payload))
	})
	return append(checks, routeChecks...), nil
}
func (s *server) runMigrationCutoverWorker(ctx context.Context) {
	if _, ok := s.store.(state.MigrationVerificationStore); !ok {
		return
	}
	store, ok := s.store.(state.MigrationCutoverStore)
	if !ok {
		return
	}
	w := migrationcutover.Worker{Store: store, Backend: func(r state.MigrationCutoverRecord) migrationcutover.Backend {
		b, _ := s.cutoverBackendForApp(state.Account{ID: r.AccountID}, state.App{ID: r.AppID, AccountID: r.AccountID, Slug: r.Report.App}, store)
		return b
	}, OnError: func(error) { s.log.Warn("production cutover check will retry") }}
	w.Run(ctx)
}
