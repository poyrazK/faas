package state

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// parked_reason was stamped once and never cleared: after a release
// recovered from a liveness park, a later `gregale park` still read "parked:
// liveness failed", and the build and deployment dependency gates kept
// treating the release as parked (2026-11-13). Waking the app now clears its
// liveness and lifecycle parks; a security park stays until its release.
func TestWakeClearsRecoverableParkReasons(t *testing.T) {
	for name, open := range map[string]func(t *testing.T) Store{
		"mem": func(*testing.T) Store { return NewMemStore() },
		"pg": func(t *testing.T) Store {
			return NewPgStore(pgtest.OpenMigrated(t))
		},
	} {
		t.Run(name, func(t *testing.T) {
			store := open(t)
			ctx := t.Context()
			account, err := store.CreateAccount(ctx, "wake-park-"+name+"@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			park := func(slug string, reason ParkReason) (App, Deployment) {
				t.Helper()
				app, err := store.CreateApp(ctx, App{AccountID: account.ID, Slug: slug, Status: AppActive})
				if err != nil {
					t.Fatal(err)
				}
				dep, err := store.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, Status: DeployPending})
				if err != nil {
					t.Fatal(err)
				}
				if err := store.SetDeploymentParked(ctx, dep.ID, string(reason), time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
				evicted := AppEvictedCold
				if _, err := store.UpdateApp(ctx, app.ID, UpdateAppParams{Status: &evicted}); err != nil {
					t.Fatal(err)
				}
				return app, dep
			}
			wake := func(app App) {
				t.Helper()
				cas := store.(interface {
					CompareAndSetAppStatus(context.Context, string, AppStatus, AppStatus) (bool, error)
				})
				if woke, err := cas.CompareAndSetAppStatus(ctx, app.ID, AppEvictedCold, AppActive); err != nil || !woke {
					t.Fatalf("wake %s: woke=%v err=%v", app.Slug, woke, err)
				}
			}
			reason := func(dep Deployment) string {
				t.Helper()
				got, err := store.DeploymentByID(ctx, dep.ID)
				if err != nil {
					t.Fatal(err)
				}
				return got.ParkedReason
			}

			livenessApp, livenessDep := park("liveness-"+name, ParkReasonLivenessExhausted)
			securityApp, securityDep := park("security-"+name, ParkReasonSecurityScanRegressed)
			wake(livenessApp)
			wake(securityApp)
			if got := reason(livenessDep); got != "" {
				t.Fatalf("liveness park survived the wake: %q", got)
			}
			if got := reason(securityDep); got != string(ParkReasonSecurityScanRegressed) {
				t.Fatalf("security park = %q, want it kept until its release", got)
			}
			// A later liveness park stamps afresh: the once-only stamp no longer
			// keeps the first park's reason.
			if err := store.SetDeploymentParked(ctx, livenessDep.ID, string(ParkReasonLivenessExhausted), time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			if got := reason(livenessDep); got != string(ParkReasonLivenessExhausted) {
				t.Fatalf("re-park reason = %q", got)
			}
		})
	}
}
