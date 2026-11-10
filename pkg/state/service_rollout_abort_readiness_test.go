package state_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestServiceAbortWriterRequiresBootReadyRecipient(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := scopedRollbackStore(t, backend)
			ctx := t.Context()
			a, err := s.CreateAccount(ctx, uuid.NewString()+"@abort-writer.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := s.CreateApp(ctx, state.App{AccountID: a.ID, Slug: "abort-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 5, Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeService}})
			if err != nil {
				t.Fatal(err)
			}
			create := func(rollout bool) state.Deployment {
				in := state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("a", 64)}
				if rollout {
					in.RolloutState = "rolling_out"
				}
				d, err := s.CreateDeployment(ctx, in)
				if err != nil {
					t.Fatal(err)
				}
				if err = s.MarkDeploymentLive(ctx, d.ID); err != nil {
					t.Fatal(err)
				}
				return d
			}
			old, next := create(false), create(true)
			node, err := s.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
			if err != nil {
				t.Fatal(err)
			}
			candidate, err := s.CreateInstanceWithMode(ctx, app.ID, next.ID, "running", 128, node.ID, uuid.NewString(), "service")
			if err != nil {
				t.Fatal(err)
			}
			routed, err := s.BeginServiceRolloutCutover(ctx, next.ID)
			if err != nil {
				t.Fatal(err)
			}
			handoff := routed.ServiceRolloutHandoff
			handoff.Action = state.ServiceRolloutActionAbort
			handoff.Phase = state.ServiceRolloutPhasePending
			if _, err = s.UpdateServiceRolloutHandoff(ctx, next.ID, handoff); err != nil {
				t.Fatal(err)
			}
			if _, err = s.BeginServiceRolloutAbort(ctx, next.ID); !errors.Is(err, state.ErrServiceRolloutNotReady) {
				t.Fatalf("empty recipient gained traffic: %v", err)
			}
			current, err := s.DeploymentByID(ctx, next.ID)
			if err != nil || current.TrafficPercent != 100 {
				t.Fatalf("refused reverse changed candidate traffic: %+v %v", current, err)
			}
			previous, err := s.CreateInstanceWithMode(ctx, app.ID, old.ID, "running", 128, node.ID, uuid.NewString(), "service")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.BeginServiceRolloutAbort(ctx, next.ID); err != nil {
				t.Fatalf("ready recipient rejected: %v", err)
			}
			if err = s.UpdateInstanceState(ctx, previous.ID, "stopped"); err != nil {
				t.Fatal(err)
			}
			if _, err = s.AbortServiceRollout(ctx, next.ID, "recipient lost during route adoption"); !errors.Is(err, state.ErrServiceRolloutNotReady) {
				t.Fatalf("abort finalized without recipient: %v", err)
			}
			current, err = s.DeploymentByID(ctx, next.ID)
			if err != nil || current.RolloutState != "rolling_out" || !current.ServiceRolloutHandoff.ActiveAbort() {
				t.Fatalf("abort lost its recovery record: %+v %v", current, err)
			}
			survivor, err := s.InstanceByID(ctx, candidate.ID)
			if err != nil || survivor.State != "running" {
				t.Fatalf("refused abort lost candidate capacity: %+v %v", survivor, err)
			}
		})
	}
}
