package apidserver_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/state"
)

// A rejected source request can finish after another deployment or settings
// edit. Compensation must never restore its baseline over that newer intent.
func TestSourceManifestCompensationPreservesNewerSettings(t *testing.T) {
	for _, field := range []string{"lifecycle", "scaling", "retry"} {
		for _, outcome := range []string{"restore", "newer-edit", "newer-deployment", "newer-deployment-same-value", "unrelated-edit"} {
			t.Run(field+"/"+outcome, func(t *testing.T) {
				srv, store, acct, app := apidserver.SourceRefAsyncRouteTestFixture(t)
				makeManifest := func(value int) *gregalemanifest.Manifest {
					switch field {
					case "lifecycle":
						return &gregalemanifest.Manifest{Lifecycle: &gregalemanifest.LifecycleConfig{RequestTimeoutS: &value}}
					case "scaling":
						return &gregalemanifest.Manifest{Scaling: &gregalemanifest.ScalingConfig{MaxInstances: &value}}
					default:
						return &gregalemanifest.Manifest{RetryPolicy: &gregalemanifest.RetryPolicyConfig{MaxAttempts: value, BaseSeconds: 1, MaxSeconds: 5}}
					}
				}
				staged, problem := srv.ApplySourceRefManifest(t.Context(), acct, app, makeManifest(2), "", false)
				if problem != nil {
					t.Fatal(problem)
				}
				want := 0
				if outcome == "newer-edit" || outcome == "newer-deployment" || outcome == "newer-deployment-same-value" {
					current, err := store.AppByID(t.Context(), app.ID)
					if err != nil {
						t.Fatal(err)
					}
					want = 4
					if outcome == "newer-deployment-same-value" {
						want = 2
					}
					if _, problem = srv.ApplySourceRefManifest(t.Context(), acct, current, makeManifest(want), "", false); problem != nil {
						t.Fatal(problem)
					}
					if outcome != "newer-edit" {
						if _, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:accepted-newer"}); err != nil {
							t.Fatal(err)
						}
					}
				}
				if outcome == "unrelated-edit" {
					current, err := store.AppByID(t.Context(), app.ID)
					if err != nil {
						t.Fatal(err)
					}
					manifest := current.Manifest
					manifest.Healthz = "/changed-health"
					if _, err = store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
						t.Fatal(err)
					}
				}
				if err := srv.RollbackSourceRefManifest(t.Context(), staged); err != nil {
					t.Fatal(err)
				}
				got, err := store.AppByID(t.Context(), app.ID)
				if err != nil {
					t.Fatal(err)
				}
				var value int
				switch field {
				case "lifecycle":
					value = got.Manifest.RequestTimeoutS
				case "scaling":
					if got.ScalingPolicy != nil {
						value = got.ScalingPolicy.MaxInstances
					}
				case "retry":
					var policy api.RetryPolicyDTO
					if len(got.RetryPolicyJSON) > 0 {
						if err := json.Unmarshal(got.RetryPolicyJSON, &policy); err != nil {
							t.Fatal(err)
						}
					}
					value = policy.MaxAttempts
				}
				if value != want {
					t.Fatalf("%s after compensation=%d want=%d", field, value, want)
				}
				if outcome == "unrelated-edit" && got.Manifest.Healthz != "/changed-health" {
					t.Fatal("compensation overwrote unrelated settings")
				}
			})
		}
	}
}

func TestSourceManifestApplyGuardsStaleLifecycle(t *testing.T) {
	for _, changed := range []string{"targeted", "unrelated"} {
		t.Run(changed, func(t *testing.T) {
			srv, store, acct, app := apidserver.SourceRefAsyncRouteTestFixture(t)
			newer := app.Manifest
			newer.Healthz = "/new-health"
			if changed == "targeted" {
				newer.RequestTimeoutS = 120
			}
			if _, err := store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Manifest: &newer}); err != nil {
				t.Fatal(err)
			}
			timeout := 10
			staged, problem := srv.ApplySourceRefManifest(t.Context(), acct, app, &gregalemanifest.Manifest{Lifecycle: &gregalemanifest.LifecycleConfig{RequestTimeoutS: &timeout}}, "", false)
			if (problem != nil) != (changed == "targeted") {
				t.Fatalf("problem=%+v", problem)
			}
			got, err := store.AppByID(t.Context(), app.ID)
			if err != nil {
				t.Fatal(err)
			}
			expected := 10
			if changed == "targeted" {
				expected = 120
			}
			if got.Manifest.RequestTimeoutS != expected || got.Manifest.Healthz != "/new-health" {
				t.Fatalf("stale source replaced newer settings: %+v", got.Manifest)
			}
			if err := srv.RollbackSourceRefManifest(t.Context(), staged); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSourceManifestCompensationPreservesNewerScalingSignals(t *testing.T) {
	for _, field := range []string{"target-name", "targets", "timezone", "schedules"} {
		t.Run(field, func(t *testing.T) {
			srv, store, acct, app := apidserver.SourceRefAsyncRouteTestFixture(t)
			maximum := 2
			scaling := &gregalemanifest.ScalingConfig{
				MaxInstances: &maximum,
				Target:       &gregalemanifest.ScalingTarget{Metric: "custom", Name: "pending_jobs", Value: 10},
				Timezone:     "UTC",
				Schedules:    []gregalemanifest.ScalingSchedule{{Cron: "0 8 * * 1-5", Duration: "1h", MinInstances: 1}},
			}
			if field == "targets" {
				scaling.Targets = []gregalemanifest.ScalingTarget{*scaling.Target}
				scaling.Target = nil
			}
			staged, problem := srv.ApplySourceRefManifest(t.Context(), acct, app, &gregalemanifest.Manifest{Scaling: scaling}, "", false)
			if problem != nil {
				t.Fatal(problem)
			}
			current, err := store.AppByID(t.Context(), app.ID)
			if err != nil {
				t.Fatal(err)
			}
			newer := *current.ScalingPolicy
			switch field {
			case "target-name":
				target := *newer.Target
				target.Name = "active_jobs"
				newer.Target = &target
			case "targets":
				newer.Targets = append([]state.ScalingTarget(nil), newer.Targets...)
				newer.Targets[0].Value = 20
			case "timezone":
				newer.Timezone = "Europe/Istanbul"
			case "schedules":
				newer.Schedules = append([]state.ScalingSchedule(nil), newer.Schedules...)
				newer.Schedules[0].DurationS = 7200
			}
			if _, err := store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{ScalingPolicy: &newer, SetScalingPolicy: true}); err != nil {
				t.Fatal(err)
			}
			if err := srv.RollbackSourceRefManifest(t.Context(), staged); err != nil {
				t.Fatal(err)
			}
			got, err := store.AppByID(t.Context(), app.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.ScalingPolicy, &newer) {
				t.Fatalf("compensation replaced newer %s: got=%+v want=%+v", field, got.ScalingPolicy, newer)
			}
		})
	}
}
