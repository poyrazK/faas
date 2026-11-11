// adr: 117, 20261020085706 — retry admission uses preserved release intent.
package apidserver_test

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRetryUsesRequestedTrafficForApproval(t *testing.T) {
	for _, percent := range []int{0, 10, 100} {
		t.Run(fmt.Sprint(percent), func(t *testing.T) {
			e := apidserver.Setup(t, api.PlanScale)
			store, acct := *e.Store(), *e.Acct()
			app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "retry-intent", Type: state.AppTypeApp, Status: state.AppActive})
			if err != nil {
				t.Fatal(err)
			}
			guardLiveDeployment(t, store, app.ID, api.DefaultEnvScope)
			dep, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "example/api@sha256:" + strings.Repeat("a", 64), TrafficPercent: percent, TrafficPercentExplicit: true})
			if err != nil {
				t.Fatal(err)
			}
			dep, err = store.SetDeploymentFailed(t.Context(), dep.ID, api.CodeDeployFailed, "temporary failure")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = store.SetReleaseApprovalPolicy(t.Context(), state.ReleaseApprovalPolicy{AppID: app.ID, AccountID: acct.ID, RequiredApprovals: 1, AfterStage: 1, TimeoutMinutes: 60, Approvers: state.ReleaseApproversAdmin}, time.Now()); err != nil {
				t.Fatal(err)
			}
			response := e.Do(t, http.MethodPost, "/v1/deployments/"+dep.ID+"/retry", api.RetryDeploymentRequest{FromStage: "source_download"}, nil)
			if percent == 0 {
				if response.Code != http.StatusAccepted {
					t.Fatalf("intentional dark retry: %d %s", response.Code, response.Body)
				}
			} else if response.Code != http.StatusConflict || guardProblemCode(t, response.Body.Bytes()) != api.CodeReleaseApprovalCanaryRequired {
				t.Fatalf("public retry bypassed approvals using its failed weight: %d %s", response.Code, response.Body)
			}
		})
	}
}

func TestRetryServiceGateSurvivesAPIEnqueue(t *testing.T) {
	for _, source := range []bool{false, true} {
		t.Run(fmt.Sprint(source), func(t *testing.T) {
			e := apidserver.Setup(t, api.PlanScale)
			t.Setenv("FAAS_STORAGE_BACKEND", "local")
			t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
			store, acct := *e.Store(), *e.Acct()
			app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "retry-service", Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 5, Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeService, ServiceReplicas: &state.ServiceReplicas{Min: 1, Max: 3, Desired: 2}}})
			if err != nil {
				t.Fatal(err)
			}
			input := state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "example/api@sha256:" + strings.Repeat("a", 64), RolloutState: "rolling_out"}
			now := time.Now().UTC()
			input.RolloutStartedAt = &now
			if source {
				// Reuse the retained-source fixture's real spool file; this deployment
				// is separate so the service marker is captured at admission.
				retained := guardFailedSourceDeployment(t, store, app.ID, "")
				input.Kind, input.SourcePath, input.SourceBytes = state.DeploymentKindTarball, retained.SourcePath, retained.SourceBytes
			}
			dep, err := store.CreateDeployment(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			dep, err = store.SetDeploymentFailed(t.Context(), dep.ID, api.CodeDeployFailed, "temporary failure")
			if err != nil {
				t.Fatal(err)
			}
			retried, err := (*e.S()).EnqueueRetry(t.Context(), app, dep, state.StageReadiness)
			if err != nil {
				t.Fatal(err)
			}
			if !state.IsServiceRollout(retried) || retried.TrafficPercent != 0 {
				t.Fatalf("retry skipped service gate: %+v", retried)
			}
			if source && retried.ImageDigest != "" {
				t.Fatalf("source retry kept derived digest: %s", retried.ImageDigest)
			}
			if !source && retried.ImageDigest != dep.ImageDigest {
				t.Fatalf("image retry changed its reviewed image: %s", retried.ImageDigest)
			}
			if source {
				if _, err := store.BuildByDeployment(t.Context(), retried.ID); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestLegacyRetryWithoutTrafficIntentReturnsConflict(t *testing.T) {
	e := apidserver.Setup(t, api.PlanScale)
	store, acct := *e.Store(), *e.Acct()
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "retry-legacy", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	dep := state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployFailed, TrafficPercentExplicit: true}
	_, err = (*e.S()).EnqueueRetry(t.Context(), app, dep, state.StageReadiness)
	var problem *api.Problem
	if !errors.As(err, &problem) || problem.Status != http.StatusConflict || !strings.Contains(problem.Detail, "fresh deployment") {
		t.Fatalf("unrecoverable legacy intent: %v", err)
	}
}
