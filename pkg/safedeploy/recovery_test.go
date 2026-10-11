package safedeploy

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type exactRecordingRecovery struct {
	recordingAPID
	exactCalls    int
	candidateID   string
	predecessorID string
	exactErr      error
}

func (r *exactRecordingRecovery) RecoverDeploymentRolloutAndIdempotencyKey(_ context.Context, candidateID, predecessorID, action, reason, key string) (api.RolloutTransitionResponse, error) {
	r.exactCalls++
	r.candidateID, r.predecessorID = candidateID, predecessorID
	r.lastRecoverAction, r.lastRecoverReason, r.lastIdempotencyKey = action, reason, key
	return api.RolloutTransitionResponse{}, r.exactErr
}

type exactRecoveryTargets struct {
	*recordingTargetResolver
	live []state.Deployment
	err  error
}

func (r *exactRecoveryTargets) LiveDeployments(context.Context, string) ([]state.Deployment, error) {
	return r.live, r.err
}

func exactRecoveryFixture() (*exactRecoveryTargets, state.Deployment) {
	base := activeTargetResolver()
	candidate := base.deployments[0]
	candidate.CreatedAt = time.Now()
	candidate.Scope = "production"
	candidate.TrafficPercent = 25
	base.deployments[0] = candidate
	return &exactRecoveryTargets{recordingTargetResolver: base, live: []state.Deployment{
		candidate,
		{ID: "stable", AppID: candidate.AppID, Status: state.DeployLive, Scope: candidate.Scope, TrafficPercent: 75, CreatedAt: candidate.CreatedAt.Add(-time.Minute)},
		{ID: "other-scope", AppID: candidate.AppID, Status: state.DeployLive, Scope: "preview", TrafficPercent: 100, CreatedAt: candidate.CreatedAt.Add(-time.Minute)},
	}}, candidate
}

func TestExactCanaryRecoveryAutomaticCallers(t *testing.T) {
	for _, caller := range []string{"alert demote", "stuck rollout"} {
		t.Run(caller, func(t *testing.T) {
			targets, candidate := exactRecoveryFixture()
			client := &exactRecordingRecovery{}
			key := "safedeploy/" + candidate.ID + "/demote/stable"
			if caller == "alert demote" {
				d := NewActionDispatcher(client, discardLog(), "meterd:safedeploy").WithTargetResolver(targets)
				rule := sampleRule()
				rule.Action = state.AlertActionDemote
				if err := d.Execute(t.Context(), rule, 42.5, time.Now()); err != nil {
					t.Fatal(err)
				}
			} else {
				o := &Orchestrator{Recovery: client, Targets: targets, Log: discardLog()}
				var stats Stats
				o.autoAbort(t.Context(), candidate, time.Hour, &stats)
				if stats.AutoAborted != 1 || stats.AutoAbortFailed != 0 {
					t.Fatalf("abort stats: %+v", stats)
				}
				key = "safedeploy/" + candidate.ID + "/stuck-abort/stable"
			}
			if client.exactCalls != 1 || client.candidateID != candidate.ID || client.predecessorID != "stable" || client.lastRecoverAction != "abort" || client.lastRecoverReason == "" || client.lastIdempotencyKey != key {
				t.Fatalf("incorrect exact recovery: %+v", client)
			}
			if client.recoverCalls != 0 || client.rollbackCalls != 0 || client.patchCalls != 0 {
				t.Fatalf("exact recovery used a legacy mutation: %+v", client)
			}
		})
	}
}

func TestExactCanaryRecoverySelectionFailsClosed(t *testing.T) {
	transportErr := errors.New("binding evidence unavailable")
	for _, scenario := range []string{"ambiguous", "missing", "newer", "active predecessor", "reader unavailable", "read error", "recovery error"} {
		t.Run(scenario, func(t *testing.T) {
			targets, candidate := exactRecoveryFixture()
			client := &exactRecordingRecovery{}
			var resolver RolloutTargetResolver = targets
			want := ErrActionTargetUnavailable
			wantCalls := 0
			switch scenario {
			case "ambiguous":
				second := targets.live[1]
				second.ID = "another-stable"
				targets.live = append(targets.live, second)
				want = ErrActionTargetAmbiguous
			case "missing":
				targets.live = []state.Deployment{candidate}
			case "newer":
				targets.live[1].CreatedAt = candidate.CreatedAt.Add(time.Second)
			case "active predecessor":
				targets.live[1].RolloutState = "rolling_out"
				targets.live[1].CanaryStep, targets.live[1].CanaryTotalSteps = 1, 4
			case "reader unavailable":
				resolver = targets.recordingTargetResolver
			case "read error":
				targets.err = transportErr
			case "recovery error":
				client.exactErr = transportErr
				want, wantCalls = transportErr, 1
			}
			d := NewActionDispatcher(client, discardLog(), "meterd:safedeploy").WithTargetResolver(resolver)
			rule := sampleRule()
			rule.Action = state.AlertActionDemote
			if err := d.Execute(t.Context(), rule, 42.5, time.Now()); !errors.Is(err, want) {
				t.Fatalf("error = %v; want %v", err, want)
			}
			if client.exactCalls != wantCalls || client.recoverCalls != 0 || client.rollbackCalls != 0 || client.patchCalls != 0 {
				t.Fatalf("unexpected recovery calls: %+v", client)
			}
		})
	}
}
