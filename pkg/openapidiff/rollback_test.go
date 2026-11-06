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
