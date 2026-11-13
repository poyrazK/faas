package openapidiff

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRollbackContractPinsSelectedBaseline(t *testing.T) {
	for _, scope := range []string{"default", "prod", "production"} {
		t.Run(scope, func(t *testing.T) {
			store := state.NewMemStore()
			account, err := store.CreateAccount(t.Context(), "rollback-contract@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "rollback-contract", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 5})
			if err != nil {
				t.Fatal(err)
			}
			capture := func(scope, document string) state.Deployment {
				t.Helper()
				deployment, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("1", 64), Scope: scope})
				if err != nil {
					t.Fatal(err)
				}
				spec, err := LoadBytes([]byte(document))
				if err != nil {
					t.Fatal(err)
				}
				raw, hash, err := MarshalSnapshot(spec)
				if err != nil {
					t.Fatal(err)
				}
				if err = store.UpdateDeploymentOpenAPISnapshot(t.Context(), state.OpenAPISnapshot{DeploymentID: deployment.ID, AppID: app.ID, Scope: scope, Snapshot: raw, SHA256: hash, SchemaVersion: SnapshotSchemaVersion, CapturedAt: time.Now().UTC()}); err != nil {
					t.Fatal(err)
				}
				return deployment
			}
			before := `{"openapi":"3.1.0","paths":{"/old":{"get":{"responses":{"200":{"description":"ok"}}}}}}`
			serving := `{"openapi":"3.1.0","paths":{"/old":{"get":{"responses":{"200":{"description":"ok"}}}},"/new":{"get":{"responses":{"200":{"description":"ok"}}}}}}`
			target := capture(scope, before)
			current := capture(scope, serving)
			capture(scope, before) // A newer dark snapshot must not mask the break.
			otherScope := "production"
			if scope == otherScope {
				otherScope = "default"
			}
			other := capture(otherScope, before)
			check, err := CheckDeploymentRollback(t.Context(), store, app.ID, target.ID, current.ID, scope)
			if err != nil || !check.Diff.Blocking() || check.Baseline.DeploymentID != current.ID {
				t.Fatalf("selected serving contract was replaced: %+v %v", check, err)
			}
			if _, err = CheckDeploymentRollback(t.Context(), store, app.ID, target.ID, other.ID, scope); err == nil {
				t.Fatal("another production lane supplied the rollback baseline")
			}
			if _, err = CheckDeploymentRollback(t.Context(), store, app.ID, target.ID, "", scope); !errors.Is(err, ErrSnapshotBaselineMissing) {
				t.Fatalf("first-baseline semantics: %v", err)
			}
		})
	}
}

// ChronoLake finding 24: a revision captured before the app adopted an API
// contract has an empty snapshot; rolling back to it is not a breaking change.
func TestRollbackToPreContractRevisionIsAllowed(t *testing.T) {
	store := state.NewMemStore()
	account, err := store.CreateAccount(t.Context(), "pre-contract@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "pre-contract", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	capture := func(document string) state.Deployment {
		t.Helper()
		deployment, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("2", 64), Scope: "default"})
		if err != nil {
			t.Fatal(err)
		}
		spec, err := LoadBytes([]byte(document))
		if err != nil {
			t.Fatal(err)
		}
		raw, hash, err := MarshalSnapshot(spec)
		if err != nil {
			t.Fatal(err)
		}
		if err = store.UpdateDeploymentOpenAPISnapshot(t.Context(), state.OpenAPISnapshot{DeploymentID: deployment.ID, AppID: app.ID, Scope: "default", Snapshot: raw, SHA256: hash, SchemaVersion: SnapshotSchemaVersion, CapturedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
		return deployment
	}
	preContract := capture(`{"openapi":"3.1.0","paths":{}}`)
	current := capture(`{"openapi":"3.1.0","paths":{"/healthz":{"get":{"responses":{"200":{"description":"ok"}}}}}}`)
	check, err := CheckDeploymentRollback(t.Context(), store, app.ID, preContract.ID, current.ID, "default")
	if !errors.Is(err, ErrSnapshotBaselineMissing) || !errors.Is(err, ErrRollbackTargetUndeclared) || check.Diff.Blocking() {
		t.Fatalf("pre-contract rollback blocked: %+v %v", check.Diff, err)
	}
	// A target that declares a contract is still compared: dropping a
	// declared route remains a break.
	dropped := capture(`{"openapi":"3.1.0","paths":{"/other":{"get":{"responses":{"200":{"description":"ok"}}}}}}`)
	if check, err = CheckDeploymentRollback(t.Context(), store, app.ID, dropped.ID, current.ID, "default"); err != nil || !check.Diff.Blocking() {
		t.Fatalf("declared-route removal not blocking: %+v %v", check.Diff, err)
	}
}
