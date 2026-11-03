package datapipelines

import (
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datapipeline"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDataAPISetupReadinessUsesServingDefaultDeployment(t *testing.T) {
	for _, tc := range []struct {
		name, scope string
		status      state.DeploymentStatus
		conflict    bool
	}{
		{"failed candidate", api.DefaultEnvScope, state.DeployFailed, false},
		{"pending candidate", api.DefaultEnvScope, state.DeployPending, false},
		{"live staging", "staging", state.DeployLive, false},
		{"serving replacement", api.DefaultEnvScope, state.DeployLive, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setup, store, job := apiSetupFixture(t)
			binding, ready, err := setup.Prepare(t.Context(), job)
			if err != nil || !ready {
				t.Fatal(binding, ready, err)
			}
			job.DataAPIPlan.BindingID = binding
			deployment, build, err := setup.Deploy(t.Context(), job)
			if err != nil {
				t.Fatal(err)
			}
			job.DataAPIPlan.DeploymentID, job.DataAPIPlan.BuildID = deployment, build
			if err = store.MarkDeploymentLive(t.Context(), deployment); err != nil {
				t.Fatal(err)
			}
			newer, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: job.DataAPIPlan.AppID, Kind: state.DeploymentKindImage, Scope: tc.scope, Status: tc.status, ImageDigest: "sha256:" + strings.Repeat("2", 64)})
			if err != nil {
				t.Fatal(err)
			}
			if tc.status == state.DeployLive {
				if err = store.MarkDeploymentLive(t.Context(), newer.ID); err != nil {
					t.Fatal(err)
				}
			}
			status, endpoint, err := setup.State(t.Context(), job)
			if tc.conflict {
				if !errors.Is(err, datapipeline.ErrConflict) || endpoint != "" {
					t.Fatal("adopted a different serving deployment", status, endpoint, err)
				}
			} else if err != nil || status != "live" || endpoint != "https://shop-data.example.test" {
				t.Fatal("unrelated candidate hid the saved serving deployment", status, endpoint, err)
			}
		})
	}
}
