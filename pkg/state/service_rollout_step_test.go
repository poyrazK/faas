// adr: 208
package state_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestServiceRolloutStepsFenceCapacityAndBindingGrants(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := scopedRollbackStore(t, backend)
			ctx := t.Context()
			a, err := s.CreateAccount(ctx, uuid.NewString()+"@rollout-step.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := s.CreateApp(ctx, state.App{AccountID: a.ID, Slug: "step-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 3,
				Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeService, ServiceReplicas: &state.ServiceReplicas{Min: 1, Max: 3, Desired: 3}}})
			if err != nil {
				t.Fatal(err)
			}
			create := func(rollout bool) state.Deployment {
				t.Helper()
				in := state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("a", 64)}
				if rollout {
					in.RolloutState = "rolling_out"
				}
				d, err := s.CreateDeployment(ctx, in)
				if err != nil {
					t.Fatal(err)
				}
				if err := s.SetDeploymentRootfs(ctx, d.ID, "/test/"+d.ID, "test/"+d.ID, 4096); err != nil {
					t.Fatal(err)
				}
				if err := s.MarkDeploymentLive(ctx, d.ID); err != nil {
					t.Fatal(err)
				}
				d, err = s.DeploymentByID(ctx, d.ID)
				if err != nil {
					t.Fatal(err)
				}
				return d
			}
			old, next := create(false), create(true)
			node, err := s.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
			if err != nil {
				t.Fatal(err)
			}
			running := func(dep state.Deployment) state.Instance {
				t.Helper()
				ins, err := s.CreateInstanceWithMode(ctx, app.ID, dep.ID, "running", 128, node.ID, uuid.NewString(), "service")
				if err != nil {
					t.Fatal(err)
				}
				return ins
			}
			oldReplicas := []state.Instance{running(old), running(old), running(old)}
			candidate := running(next)
			steps := s.(state.ServiceRolloutStepStore)
			for _, percent := range []int{-1, 0, 100, 101} {
				if _, err := steps.BeginServiceRolloutStep(ctx, next.ID, percent); !errors.Is(err, state.ErrServiceRolloutInvalid) {
					t.Fatalf("invalid step %d: %v", percent, err)
				}
			}
			if _, err := steps.BeginServiceRolloutStep(ctx, next.ID, 66); !errors.Is(err, state.ErrServiceRolloutNotReady) {
				t.Fatalf("oversized step: %v", err)
			}
			for _, ins := range oldReplicas[:2] {
				if err := s.UpdateInstanceState(ctx, ins.ID, "stopped"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := steps.BeginServiceRolloutStep(ctx, next.ID, 33); !errors.Is(err, state.ErrServiceRolloutNotReady) {
				t.Fatalf("partial routing ignored predecessor readiness: %v", err)
			}
			unchanged, err := s.DeploymentByID(ctx, next.ID)
			if err != nil || unchanged.TrafficPercent != 0 {
				t.Fatalf("refused step changed weights: %+v %v", unchanged, err)
			}
			for _, ins := range oldReplicas[:2] {
				if err := s.UpdateInstanceState(ctx, ins.ID, "running"); err != nil {
					t.Fatal(err)
				}
			}
			zero := int64(0)
			if _, err := s.(state.BindingReleasePolicyStore).SetBindingReleasePolicy(ctx, a.ID, app.ID, "default", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero}); err != nil {
				t.Fatal(err)
			}
			queued, err := steps.BeginServiceRolloutStep(ctx, next.ID, 33)
			if !state.IsBindingReleaseRequired(err) || queued.ServiceRolloutHandoff.BindingsCheck.TrafficPercent != 33 {
				t.Fatalf("step did not queue exact grant: %+v %v", queued, err)
			}
			grant := func(request string) context.Context {
				f := bindingRecoveryFence(ctx, t, s.(state.BindingPromotionStore), a, app, next)
				f.PolicyRevision, f.MaxVerificationAge = 1, api.DefaultBindingVerificationAge
				return state.WithServiceRolloutBindingRequest(state.WithBindingReleaseFences(ctx, []state.BindingPromotionFence{f}), request)
			}
			request := queued.ServiceRolloutHandoff.BindingsCheck.RequestID
			if _, err := s.BeginServiceRolloutCutover(grant(request), next.ID); !errors.Is(err, state.ErrServiceRolloutInvalid) {
				t.Fatalf("partial approval authorized full cutover: %v", err)
			}
			if _, err := steps.BeginServiceRolloutStep(grant(request), next.ID, 33); err != nil {
				t.Fatal(err)
			}
			if err := s.UpdateInstanceState(ctx, candidate.ID, "stopped"); err != nil {
				t.Fatal(err)
			}
			if _, err := steps.ClaimServiceRolloutReplicaDrains(ctx, next.ID, 33); !errors.Is(err, state.ErrServiceRolloutNotReady) {
				t.Fatalf("withdrawn candidate released predecessor: %v", err)
			}
			if err := s.UpdateInstanceState(ctx, candidate.ID, "running"); err != nil {
				t.Fatal(err)
			}
			claimed, err := steps.ClaimServiceRolloutReplicaDrains(ctx, next.ID, 33)
			if err != nil || len(claimed.ServiceRolloutHandoff.RetiringInstanceIDs) != 1 {
				t.Fatalf("partial withdrawal: %+v %v", claimed, err)
			}
			if err := steps.CompleteServiceRolloutReplicaDrains(ctx, next.ID, claimed.ServiceRolloutHandoff.RetiringInstanceIDs); !errors.Is(err, state.ErrServiceRolloutInvalid) {
				t.Fatalf("cleared resident withdrawal: %v", err)
			}
			if _, err := s.UpdateServiceRolloutHandoff(ctx, next.ID, queued.ServiceRolloutHandoff); err != nil {
				t.Fatal(err)
			}
			current, err := s.DeploymentByID(ctx, next.ID)
			if err != nil || len(current.ServiceRolloutHandoff.RetiringInstanceIDs) != 1 {
				t.Fatalf("stale scheduler erased withdrawal: %+v %v", current, err)
			}
			retired, err := s.InstanceByID(ctx, claimed.ServiceRolloutHandoff.RetiringInstanceIDs[0])
			if err != nil || retired.State != "draining" || !state.State(retired.State).CountsForRAM() {
				t.Fatalf("withdrawal lost reservation: %+v %v", retired, err)
			}
			again, err := steps.ClaimServiceRolloutReplicaDrains(ctx, next.ID, 33)
			if err != nil || len(again.ServiceRolloutHandoff.RetiringInstanceIDs) != 1 || again.ServiceRolloutHandoff.RetiringInstanceIDs[0] != retired.ID {
				t.Fatalf("retry changed withdrawal: %+v %v", again, err)
			}
			if _, err := steps.ClaimServiceRolloutReplicaDrains(ctx, next.ID, 66); !errors.Is(err, state.ErrServiceRolloutInvalid) {
				t.Fatalf("stale percentage claimed drains: %v", err)
			}
			running(next)
			queued, err = steps.BeginServiceRolloutStep(ctx, next.ID, 66)
			if !state.IsBindingReleaseRequired(err) || queued.ServiceRolloutHandoff.BindingsCheck.RequestID == request {
				t.Fatalf("new step reused grant: %+v %v", queued, err)
			}
			if _, err := steps.BeginServiceRolloutStep(grant(request), next.ID, 66); !errors.Is(err, state.ErrServiceRolloutInvalid) {
				t.Fatalf("stale grant authorized next step: %v", err)
			}
		})
	}
}
