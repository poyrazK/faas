package state_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSnapshotPrimePublicationRejectsObsoleteAttempts(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := scopedRollbackStore(t, backend)
			for _, change := range []string{"unchanged", "superseded", "requeued"} {
				t.Run(change, func(t *testing.T) {
					ctx := t.Context()
					a, err := s.CreateAccount(ctx, uuid.NewString()+"@prime-fence.test", api.PlanPro)
					if err != nil {
						t.Fatal(err)
					}
					app, err := s.CreateApp(ctx, state.App{AccountID: a.ID, Slug: "prime-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 5})
					if err != nil {
						t.Fatal(err)
					}
					d, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeploySnapshotting, ImageDigest: "sha256:" + strings.Repeat("a", 64)})
					if err != nil {
						t.Fatal(err)
					}
					if err = s.SetDeploymentRootfs(ctx, d.ID, "/test/"+d.ID, "test/"+d.ID, 4096); err != nil {
						t.Fatal(err)
					}
					if _, err = s.AppendDeploymentStage(ctx, d.ID, state.StageSourceDownload, state.StageSnapshotPrepare, time.Now().Add(-time.Minute), ""); err != nil {
						t.Fatal(err)
					}
					d, err = s.DeploymentByID(ctx, d.ID)
					if err != nil {
						t.Fatal(err)
					}
					primeCtx, err := state.WithSnapshotPrimeAttempt(ctx, d)
					if err != nil {
						t.Fatal(err)
					}
					node, err := s.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
					if err != nil {
						t.Fatal(err)
					}
					ins, err := s.CreateInstanceWithMode(ctx, app.ID, d.ID, "cold_booting", 128, node.ID, uuid.NewString(), "normal")
					if err != nil {
						t.Fatal(err)
					}
					values, err := s.(state.RuntimeAppValuesStore).RuntimeAppValuesForDeployment(ctx, a.ID, app.ID, d.ID)
					if err != nil {
						t.Fatal(err)
					}
					fence, err := state.NewRuntimeAppConfigFence(values)
					if err != nil {
						t.Fatal(err)
					}
					pub := state.RuntimeInstancePublication{AccountID: a.ID, AppID: app.ID, InstanceID: ins.ID, NodeID: ins.NodeID, WakeID: ins.WakeID, ExpectedState: "cold_booting", Netns: "test-prime", HostIP: "10.100.0.8", GuestUID: 20001, Fence: fence.SecretFence, ConfigFence: fence}
					if change != "unchanged" {
						if err = s.MarkDeploymentSuperseded(ctx, d.ID); err != nil {
							t.Fatal(err)
						}
					}
					if change == "requeued" {
						if _, err = s.PrepareDeploymentRollback(ctx, app.ID, d.ID); err != nil {
							t.Fatal(err)
						}
					}
					_, err = s.(state.RuntimeInstancePublicationStore).PublishOwnedInstanceRuntime(primeCtx, pub)
					if change == "unchanged" {
						if err != nil {
							t.Fatal(err)
						}
						return
					}
					if !errors.Is(err, state.ErrSnapshotPrimeStale) {
						t.Fatalf("stale %s attempt published: %v", change, err)
					}
					got, err := s.InstanceByID(ctx, ins.ID)
					if err != nil || got.State != "cold_booting" || got.Netns != "" {
						t.Fatalf("refused publication mutated provisional instance: %+v %v", got, err)
					}
					if _, err = s.(state.RuntimeInstancePublicationStore).InstanceRuntimeConfigFence(ctx, a.ID, app.ID, ins.ID); !errors.Is(err, state.ErrNotFound) {
						t.Fatalf("refused publication left a config proof: %v", err)
					}
					if change == "requeued" {
						fresh, err := s.DeploymentByID(ctx, d.ID)
						if err != nil {
							t.Fatal(err)
						}
						freshCtx, err := state.WithSnapshotPrimeAttempt(ctx, fresh)
						if err != nil {
							t.Fatal(err)
						}
						freshInstance, err := s.CreateInstanceWithMode(ctx, app.ID, d.ID, "cold_booting", 128, node.ID, uuid.NewString(), "normal")
						if err != nil {
							t.Fatal(err)
						}
						pub.InstanceID, pub.WakeID = freshInstance.ID, freshInstance.WakeID
						if _, err = s.(state.RuntimeInstancePublicationStore).PublishOwnedInstanceRuntime(freshCtx, pub); err != nil {
							t.Fatalf("fresh attempt cannot publish: %v", err)
						}
					}
				})
			}
		})
	}
}
