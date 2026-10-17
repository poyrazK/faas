// adr:1572 — resumable migration evidence over existing runtime owners.
package apidserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/migrationverify"
	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) migrationVerificationBackend(w http.ResponseWriter, r *http.Request, acct state.Account) (migrationverify.Backend, bool) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return migrationverify.Backend{}, false
	}
	store, ok := s.store.(state.MigrationVerificationStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("migration verification receipts are unavailable"))
		return migrationverify.Backend{}, false
	}
	return s.migrationBackendForApp(acct, app, store), true
}
func (s *server) createMigrationVerification(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.requireAppTaskAPI(w) {
		return
	}
	b, ok := s.migrationVerificationBackend(w, r, acct)
	if !ok {
		return
	}
	var in api.CreateMigrationVerificationRequest
	if err := decodeJSONSized(r, &in, api.AppTaskRequestMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid migration verification request"))
		return
	}
	out, err := b.Create(r.Context(), in)
	s.respondMigrationVerification(w, out, err)
}
func (s *server) getMigrationVerification(w http.ResponseWriter, r *http.Request, acct state.Account) {
	b, ok := s.migrationVerificationBackend(w, r, acct)
	if !ok {
		return
	}
	out, err := b.Get(r.Context(), r.PathValue("run"))
	s.respondMigrationVerification(w, out, err)
}
func (s *server) listMigrationVerifications(w http.ResponseWriter, r *http.Request, acct state.Account) {
	b, ok := s.migrationVerificationBackend(w, r, acct)
	if !ok {
		return
	}
	out, err := b.List(r.Context())
	s.respondMigrationVerification(w, out, err)
}
func (s *server) advanceMigrationVerification(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.requireAppTaskAPI(w) {
		return
	}
	if !acct.Active() {
		api.WriteProblem(w, acct.InactiveProblem())
		return
	}
	b, ok := s.migrationVerificationBackend(w, r, acct)
	if !ok {
		return
	}
	out, err := b.Advance(r.Context(), r.PathValue("run"))
	s.respondMigrationVerification(w, out, err)
}

func (s *server) cancelMigrationVerification(w http.ResponseWriter, r *http.Request, acct state.Account) {
	// Cancellation and recovery remain available if task admission is disabled
	// or the account becomes inactive after starting verification.
	b, ok := s.migrationVerificationBackend(w, r, acct)
	if !ok {
		return
	}
	out, err := b.Cancel(r.Context(), r.PathValue("run"))
	s.respondMigrationVerification(w, out, err)
}
func (s *server) respondMigrationVerification(w http.ResponseWriter, out any, err error) {
	if err != nil {
		var problem *api.Problem
		switch {
		case errors.As(err, &problem):
		case errors.Is(err, state.ErrNotFound):
			problem = api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Verification unavailable", "No such verification for this app.")
		case errors.Is(err, state.ErrConflict):
			problem = api.NewProblem(http.StatusConflict, api.CodeConflict, "Verification changed", "Refresh the existing verification before starting another run.")
		default:
			problem = api.ErrCapacity("Verification could not advance. Its saved progress can be resumed.")
		}
		api.WriteProblem(w, problem)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

func (s *server) migrationBackendForApp(acct state.Account, app state.App, store state.MigrationVerificationStore) migrationverify.Backend {
	return migrationverify.Backend{Store: store, Tasks: s.store, AccountID: acct.ID, AppID: app.ID, App: app.Slug,
		ConnectionsCurrent: func(ctx context.Context, connections []api.MigrationServiceConnectionCheck) error {
			return migrationverify.ValidateConnectionTargets(ctx, s.store, acct.ID, app.ID, connections)
		},
		Snapshot: func(ctx context.Context) (migrationverify.Snapshot, error) {
			return s.migrationSnapshot(ctx, acct.ID, app.ID)
		},
		Smoke: func(ctx context.Context, run api.MigrationVerification) (api.MigrationVerificationCheck, error) {
			return s.migrationSmoke(ctx, app, run)
		},
		Park: func(ctx context.Context, run api.MigrationVerification) error {
			account, err := s.store.AccountByID(ctx, acct.ID)
			if err != nil {
				return err
			}
			if !account.Active() || !s.appTaskAPIEnabled {
				return state.ErrConflict
			}
			current, err := s.store.AppByID(ctx, app.ID)
			if err != nil {
				return err
			}
			if current.Status == state.AppDeleted || current.AccountID != acct.ID {
				return state.ErrConflict
			}
			park, ok := s.store.(state.ConditionalAppParkTransitionStore)
			if !ok {
				return state.ErrConflict
			}
			_, _, err = park.BeginAppParkTransitionIfDeployment(ctx, app.ID, current.Status, run.DeploymentID)
			if err != nil {
				return err
			}
			payload, _ := json.Marshal(map[string]string{"kind": "parked", "slug": app.Slug, "app_id": app.ID})
			return s.notif.Notify(ctx, db.NotifyAppChanged, string(payload))
		},
		Drained: func(ctx context.Context) (bool, error) {
			lister, ok := s.store.(activeInstancesLister)
			if !ok {
				return false, state.ErrConflict
			}
			rows, err := lister.ListActiveInstancesForApp(ctx, app.ID, 1)
			return len(rows) == 0, err
		},
		Wake: func(ctx context.Context, run api.MigrationVerification) error {
			account, err := s.store.AccountByID(ctx, acct.ID)
			if err != nil {
				return err
			}
			if !account.Active() {
				return state.ErrConflict
			}
			current, err := s.store.AppByID(ctx, app.ID)
			if err != nil {
				return err
			}
			if current.Status == state.AppDeleted || current.AccountID != acct.ID {
				return state.ErrConflict
			}
			dep, err := s.store.LiveDeployment(ctx, app.ID)
			if err != nil {
				return err
			}
			if dep.ID != run.DeploymentID || dep.TrafficPercent != 100 {
				return state.ErrConflict
			}
			if problem := s.validateExplicitAppWake(ctx, current); problem != nil {
				return problem
			}
			payload, _ := json.Marshal(map[string]string{"app_id": app.ID, "wake_id": run.WakeID})
			return s.notif.Notify(ctx, db.NotifyAppWake, string(payload))
		},
		Running: func(ctx context.Context, run api.MigrationVerification) (string, bool, error) {
			row, err := s.store.RunningInstanceForApp(ctx, app.ID)
			if errors.Is(err, state.ErrNotFound) {
				return "", false, nil
			}
			if err != nil {
				return "", false, err
			}
			ready := row.DeploymentID == run.DeploymentID && row.WakeID == run.WakeID && run.DrainedAt != nil
			return row.ID, ready, nil
		},
		Restore: func(ctx context.Context, run api.MigrationVerification) (string, error) {
			return s.restoreMigrationService(ctx, acct.ID, app.ID, run)
		},
	}
}
func (s *server) migrationSnapshot(ctx context.Context, account, appID string) (migrationverify.Snapshot, error) {
	app, err := s.store.AppByID(ctx, appID)
	if err != nil {
		return migrationverify.Snapshot{}, err
	}
	if app.AccountID != account {
		return migrationverify.Snapshot{}, state.ErrNotFound
	}
	acct, err := s.store.AccountByID(ctx, account)
	if err != nil {
		return migrationverify.Snapshot{}, err
	}
	dep, err := s.store.LiveDeployment(ctx, appID)
	if err != nil {
		return migrationverify.Snapshot{}, err
	}
	stamp, _, err := state.RuntimeConfigChangedAtForScope(ctx, s.store, appID, dep.Scope)
	if err != nil {
		return migrationverify.Snapshot{}, err
	}
	runtimeStore, ok := s.store.(state.BindingRuntimeInventoryStore)
	if !ok {
		return migrationverify.Snapshot{}, state.ErrConflict
	}
	runtime, err := runtimeStore.ReadBindingRuntimeInventory(ctx, account, appID, dep.Scope)
	if err != nil {
		return migrationverify.Snapshot{}, err
	}
	fresh := false
	for _, row := range runtime.Deployments {
		if row.ID == dep.ID {
			fresh = row.Resident.Stale == 0 && row.Resident.Unknown == 0 && row.Starting == 0
		}
	}
	blocked := ""
	switch {
	case !acct.Active():
		blocked = "account_inactive"
	case app.Status == state.AppDeleted:
		blocked = "app_deleted"
	case !s.appTaskAPIEnabled:
		blocked = "verification_disabled"
	case dep.ParkedReason == string(state.ParkReasonSecurityScanRegressed):
		blocked = "security_posture_blocked"
	}
	return migrationverify.Snapshot{App: app, Deployment: dep, ChangedAt: stamp, RuntimeCurrent: fresh, BlockedReason: blocked}, nil
}
func (s *server) migrationSmoke(ctx context.Context, app state.App, run api.MigrationVerification) (api.MigrationVerificationCheck, error) {
	verifier := apihostingreceipt.Verifier{BaseURL: appURLForDomain(app.Slug, s.domain), Required: true, Timeout: 20 * time.Second, RequestTimeout: 5 * time.Second, RetryInterval: time.Second,
		Authorize: func(ctx context.Context, dep, token string, expires time.Time) error {
			payload, _ := json.Marshal(struct {
				AppID        string    `json:"app_id"`
				DeploymentID string    `json:"deployment_id"`
				Token        string    `json:"token"`
				ExpiresAt    time.Time `json:"expires_at"`
			}{app.ID, dep, token, expires})
			return s.notif.Notify(ctx, db.NotifyDeploymentSmokeChallenge, string(payload))
		}}
	result, err := verifier.VerifyDeployment(ctx, app.Slug, run.Request.SmokePath, run.DeploymentID)
	check := api.MigrationVerificationCheck{Status: "failed", Detail: "The selected route did not return a proven successful response. Check the path, application logs and dependencies."}
	if err != nil {
		return check, api.ErrCapacity("The gateway could not verify this route. Resume this saved verification.")
	}
	if result.Status == apihostingreceipt.SmokeVerified {
		check.Status = "passed"
		check.Detail = "The selected deployment returned a successful response through the Gregale gateway."
	}
	return check, nil
}
