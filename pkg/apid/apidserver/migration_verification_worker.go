// adr:1573 — server-driven migration checks and durable restoration.
package apidserver

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/migrationverify"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) migrationVerificationWorker() (migrationverify.Worker, bool) {
	workerStore, ok := s.store.(state.MigrationVerificationWorkerStore)
	if !ok {
		return migrationverify.Worker{}, false
	}
	store, ok := s.store.(state.MigrationVerificationStore)
	if !ok {
		return migrationverify.Worker{}, false
	}
	return migrationverify.Worker{Store: workerStore,
		Backend: func(_ context.Context, r state.MigrationVerificationRecord) (migrationverify.Backend, error) {
			return s.migrationBackendForApp(state.Account{ID: r.AccountID}, state.App{ID: r.AppID, AccountID: r.AccountID, Slug: r.Report.App}, store), nil
		},
		OnError: func(error) { s.log.Warn("migration verification step will retry") },
	}, true
}

func (s *server) runMigrationVerificationWorker(ctx context.Context) {
	if worker, ok := s.migrationVerificationWorker(); ok {
		worker.Run(ctx)
	}
}

// restoreMigrationService never qualifies evidence. It restores availability
// through the existing wake owner, and refuses to wake a replaced or prohibited
// deployment. Success requires a running instance, not a published notification.
func (s *server) restoreMigrationService(ctx context.Context, account, appID string, run api.MigrationVerification) (string, error) {
	app, err := s.store.AppByID(ctx, appID)
	if errors.Is(err, state.ErrNotFound) {
		return "superseded", nil
	}
	if err != nil {
		return "", err
	}
	if app.AccountID != account || app.Status == state.AppDeleted {
		return "superseded", nil
	}
	acct, err := s.store.AccountByID(ctx, account)
	if err != nil {
		return "", err
	}
	if !acct.Active() {
		return "blocked", nil
	}
	dep, err := s.store.LiveDeployment(ctx, appID)
	if errors.Is(err, state.ErrNotFound) {
		return "superseded", nil
	}
	if err != nil {
		return "", err
	}
	if dep.ID != run.DeploymentID || dep.TrafficPercent != 100 {
		return "superseded", nil
	}
	if dep.ParkedReason == string(state.ParkReasonSecurityScanRegressed) {
		return "blocked", nil
	}
	if app.Status != state.AppEvictedCold {
		row, err := s.store.RunningInstanceForApp(ctx, appID)
		if err == nil && row.DeploymentID == dep.ID {
			return "restored", nil
		}
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			return "", err
		}
	} else {
		lister, ok := s.store.(activeInstancesLister)
		if !ok {
			return "", state.ErrConflict
		}
		rows, err := lister.ListActiveInstancesForApp(ctx, appID, 1)
		if err != nil {
			return "", err
		}
		if len(rows) > 0 {
			payload, _ := json.Marshal(map[string]string{"kind": "parked", "slug": app.Slug, "app_id": app.ID})
			return "pending", s.notif.Notify(ctx, db.NotifyAppChanged, string(payload))
		}
	}
	// Retry the original identity after dropped LISTEN delivery or API restart.
	if err := s.validateExplicitAppWake(ctx, app); err != nil {
		return "", err
	}
	payload, _ := json.Marshal(map[string]string{"app_id": app.ID, "wake_id": run.WakeID})
	return "pending", s.notif.Notify(ctx, db.NotifyAppWake, string(payload))
}
