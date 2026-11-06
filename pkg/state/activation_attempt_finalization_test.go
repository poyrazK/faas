package state_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func activationProofFixture(t *testing.T, s state.Store, mode string) (state.App, state.Deployment, state.Instance) {
	t.Helper()
	ctx := t.Context()
	a, err := s.CreateAccount(ctx, uuid.NewString()+"@activation-proof.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: a.ID, Slug: "proof-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 5, Manifest: state.AppManifest{ExecutionMode: mode}})
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("d", 64), Status: state.DeploySnapshotting})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeploymentRootfs(ctx, d.ID, "/test/"+d.ID, "test/"+d.ID, 4096); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateDeploymentStatus(ctx, d.ID, state.DeploySnapshotting, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendDeploymentStage(ctx, d.ID, state.StageSourceDownload, state.StageSnapshotPrepare, time.Now().UTC(), ""); err != nil {
		t.Fatal(err)
	}
	d, err = s.DeploymentByID(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertAppSecretWithClassInScope(ctx, app.AccountID, app.ID, api.DefaultEnvScope, "TOKEN", "key", "hash", state.SecretClassEphemeral, []byte("sealed")); err != nil {
		t.Fatal(err)
	}
	node, err := s.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	insMode := string(state.InstanceModeNormal)
	if mode == api.ExecutionModeService {
		insMode = string(state.InstanceModeService)
	}
	i, err := s.CreateInstanceWithMode(ctx, app.ID, d.ID, "cold_booting", app.RAMMB, node.ID, uuid.NewString(), insMode)
	if err != nil {
		t.Fatal(err)
	}
	values, err := s.RuntimeAppValuesForDeployment(ctx, app.AccountID, app.ID, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	fence, err := state.NewRuntimeAppConfigFence(values)
	if err != nil {
		t.Fatal(err)
	}
	i, err = s.(state.RuntimeInstancePublicationStore).PublishOwnedInstanceRuntime(ctx, state.RuntimeInstancePublication{AccountID: app.AccountID, AppID: app.ID, InstanceID: i.ID, NodeID: i.NodeID, WakeID: i.WakeID, ExpectedState: "cold_booting", Netns: "activation-proof", HostIP: "10.0.0.1", GuestUID: 20001, Fence: fence.SecretFence, ConfigFence: fence})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateInstanceState(ctx, i.ID, "stopped"); err != nil {
		t.Fatal(err)
	}
	i, err = s.InstanceByID(ctx, i.ID)
	if err != nil {
		t.Fatal(err)
	}
	return app, d, i
}

func TestEphemeralActivationChecksFinalWrite(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := scopedRollbackStore(t, backend)
			for _, mode := range []string{api.ExecutionModeRequest, api.ExecutionModeService} {
				t.Run(mode, func(t *testing.T) {
					for _, change := range []string{"current", "environment", "requeued", "artifact", "wrong-wake", "wrong-node", "missing-proof"} {
						t.Run(change, func(t *testing.T) {
							ctx := t.Context()
							app, d, i := activationProofFixture(t, s, mode)
							source := i
							switch change {
							case "wrong-wake":
								source.WakeID = uuid.NewString()
							case "wrong-node":
								source.NodeID = uuid.NewString()
							case "missing-proof":
								var err error
								source, err = s.CreateInstanceWithMode(ctx, app.ID, d.ID, "stopped", app.RAMMB, i.NodeID, uuid.NewString(), i.Mode)
								if err != nil {
									t.Fatal(err)
								}
							}
							guarded, err := state.WithEphemeralDeploymentActivation(ctx, d, source)
							if err != nil {
								t.Fatal(err)
							}
							switch change {
							case "environment":
								err = s.UpsertAppEnv(ctx, app.AccountID, app.ID, "VERSION", "new")
							case "requeued":
								err = s.MarkDeploymentSuperseded(ctx, d.ID)
								if err == nil {
									_, err = s.PrepareDeploymentRollback(ctx, app.ID, d.ID)
								}
							case "artifact":
								err = s.SetDeploymentRootfs(ctx, d.ID, "/new/rootfs", "new/rootfs", 4096)
							}
							if err != nil {
								t.Fatal(err)
							}
							before, err := s.DeploymentByID(ctx, d.ID)
							if err != nil {
								t.Fatal(err)
							}
							err = s.MarkDeploymentLive(guarded, d.ID)
							after, readErr := s.DeploymentByID(ctx, d.ID)
							if readErr != nil {
								t.Fatal(readErr)
							}
							if change == "current" {
								if err != nil || after.Status != state.DeployLive {
									t.Fatalf("current stopped proof rejected: %v status=%s", err, after.Status)
								}
								return
							}
							if err == nil || before.Status != after.Status || before.TrafficPercent != after.TrafficPercent || !bytes.Equal(before.StageState, after.StageState) {
								t.Fatalf("stale stopped proof changed deployment: before=%s after=%s err=%v", before.Status, after.Status, err)
							}
						})
					}
				})
			}
		})
	}
}

func TestGenericFailureChecksActivationAttempt(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := scopedRollbackStore(t, backend)
			for _, writer := range []string{"plain", "extended"} {
				t.Run(writer, func(t *testing.T) {
					for _, change := range []string{"current", "environment", "requeued", "artifact", "cancelled", "promoted", "unguarded", "stale-recipient"} {
						t.Run(change, func(t *testing.T) {
							ctx := t.Context()
							app, d, source := activationProofFixture(t, s, api.ExecutionModeRequest)
							values, err := s.RuntimeAppValuesForDeployment(ctx, app.AccountID, app.ID, d.ID)
							if err != nil {
								t.Fatal(err)
							}
							guarded, err := state.WithDeploymentActivationAttempt(ctx, d, values)
							if err != nil {
								t.Fatal(err)
							}
							if change == "unguarded" {
								guarded = ctx
							}
							if change == "stale-recipient" {
								source.WakeID = uuid.NewString()
								guarded, err = state.WithEphemeralDeploymentActivation(guarded, d, source)
								if err != nil {
									t.Fatal(err)
								}
							}
							switch change {
							case "environment", "unguarded":
								err = s.UpsertAppEnv(ctx, app.AccountID, app.ID, "VERSION", "new")
							case "requeued":
								err = s.MarkDeploymentSuperseded(ctx, d.ID)
								if err == nil {
									_, err = s.PrepareDeploymentRollback(ctx, app.ID, d.ID)
								}
							case "artifact":
								err = s.SetDeploymentRootfs(ctx, d.ID, "/new/rootfs", "new/rootfs", 4096)
							case "cancelled":
								_, _, err = s.CancelDeploymentTx(ctx, d.ID, "test", state.CancelReasonUser)
							case "promoted":
								err = s.MarkDeploymentLive(ctx, d.ID)
							}
							if err != nil {
								t.Fatal(err)
							}
							before, err := s.DeploymentByID(ctx, d.ID)
							if err != nil {
								t.Fatal(err)
							}
							if writer == "plain" {
								_, err = s.SetDeploymentFailed(guarded, d.ID, api.CodeDeployFailed, "old activation failed")
							} else {
								_, err = s.SetDeploymentFailedEx(guarded, d.ID, api.CodeDeployFailed, "old activation failed", "hint", "why", "fix", nil)
							}
							after, readErr := s.DeploymentByID(ctx, d.ID)
							if readErr != nil {
								t.Fatal(readErr)
							}
							if change == "current" || change == "unguarded" {
								if err != nil || after.Status != state.DeployFailed || after.ErrorCode != api.CodeDeployFailed {
									t.Fatalf("current/legacy failure rejected: %+v %v", after, err)
								}
								return
							}
							if !errors.Is(err, state.ErrDeploymentActivationStale) || before.Status != after.Status || before.TrafficPercent != after.TrafficPercent || before.ErrorCode != after.ErrorCode || before.ErrorHint != after.ErrorHint || !bytes.Equal(before.StageState, after.StageState) {
								t.Fatalf("stale failure modified deployment: before=%s after=%s err=%v", before.Status, after.Status, err)
							}
						})
					}
				})
			}
		})
	}
}
