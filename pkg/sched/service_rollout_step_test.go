// adr: 208
package sched_test

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

type boundedRolloutNotifier struct{}

func (boundedRolloutNotifier) Notify(context.Context, string, string) error { return nil }

type boundedRolloutVM struct {
	sched.RoutedVMM
	mu        sync.Mutex
	boots     int
	onDestroy func(context.Context, string) error
}

func (v *boundedRolloutVM) CreateColdBoot(_ context.Context, _ string, id string, _ sched.AppSpec) (*sched.WakeOutcome, error) {
	v.mu.Lock()
	v.boots++
	v.mu.Unlock()
	return &sched.WakeOutcome{Instance: id, Netns: "bounded-" + id, HostIP: "10.0.0.1", LeaseUID: 20001}, nil
}
func (v *boundedRolloutVM) Destroy(ctx context.Context, _ string, id string) error {
	if v.onDestroy != nil {
		return v.onDestroy(ctx, id)
	}
	return nil
}
func (*boundedRolloutVM) PauseAndSnapshot(context.Context, string, string, string, string, string, bool) (sched.SnapshotBytes, error) {
	return sched.SnapshotBytes{MemBytes: 128 << 20, VMStateBytes: 4096, StoredBytes: 1 << 20}, nil
}
func (*boundedRolloutVM) StopInstanceOnNode(context.Context, string, string, int32, int32) (*sched.StopInstanceOutcome, error) {
	return &sched.StopInstanceOutcome{}, nil
}

func TestServiceRolloutCompletesWithinConcurrencyBudget(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			if backend == "postgres" {
				store = state.NewPgStore(pgtest.OpenMigrated(t))
			}
			for _, tc := range []struct {
				name           string
				desired, limit int
				plan           api.Plan
			}{
				{"one-replica-control", 1, 2, api.PlanPro},
				{"two-replicas-one-surge-slot", 2, 3, api.PlanPro},
				{"two-replicas-at-concurrency-cap", 2, 2, api.PlanPro},
				{"three-replicas-at-concurrency-cap", 3, 3, api.PlanPro},
				{"three-replicas-at-pro-plan-cap", 3, api.MustLimitsFor(api.PlanPro).MaxConcurrency, api.PlanPro},
				{"two-replicas-full-cohort-headroom", 2, 4, api.PlanPro},
				{"five-replicas-at-concurrency-cap", 5, 5, api.PlanScale},
			} {
				t.Run(tc.name, func(t *testing.T) {
					ctx := t.Context()
					acct, err := store.CreateAccount(ctx, uuid.NewString()+"@rollout-step.test", tc.plan)
					if err != nil {
						t.Fatal(err)
					}
					app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "bounded-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: tc.limit,
						Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeService, ServiceReplicas: &state.ServiceReplicas{Min: 1, Max: tc.desired, Desired: tc.desired}}})
					if err != nil {
						t.Fatal(err)
					}
					create := func(candidate bool) state.Deployment {
						t.Helper()
						d := state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("a", 64)}
						if candidate {
							d.RolloutState = "rolling_out"
						}
						d, err = store.CreateDeployment(ctx, d)
						if err != nil {
							t.Fatal(err)
						}
						if err := store.SetDeploymentRootfs(ctx, d.ID, "/review/"+d.ID, "review/"+d.ID, 4096); err != nil {
							t.Fatal(err)
						}
						if err := store.MarkDeploymentLive(ctx, d.ID); err != nil {
							t.Fatal(err)
						}
						d, err = store.DeploymentByID(ctx, d.ID)
						if err != nil {
							t.Fatal(err)
						}
						return d
					}
					old := create(false)
					node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
					if err != nil {
						t.Fatal(err)
					}
					for n := 0; n < tc.desired; n++ {
						if _, err := store.CreateInstanceWithMode(ctx, app.ID, old.ID, "running", 128, node.ID, uuid.NewString(), "service"); err != nil {
							t.Fatal(err)
						}
					}
					candidate := create(true)
					vm := &boundedRolloutVM{}
					if tc.name == "two-replicas-at-concurrency-cap" {
						// Another owner can confirm STOPPED while this RPC completes.
						// The local reservation must still be released after a lost CAS.
						vm.onDestroy = func(ctx context.Context, id string) error {
							ins, err := store.InstanceByID(ctx, id)
							if err != nil {
								return err
							}
							if ins.State == "draining" {
								return store.UpdateInstanceState(ctx, id, "stopped")
							}
							return nil
						}
					}
					e, err := sched.NewEngine(ctx, store, sched.NewNodeLedger(), vm, boundedRolloutNotifier{}, "1.10.0", slog.New(slog.NewTextHandler(io.Discard, nil)))
					if err != nil {
						t.Fatal(err)
					}
					*e.ServiceReconcileSubmit() = func(context.Context, string) {}
					if err := e.SeedLedger(ctx); err != nil {
						t.Fatal(err)
					}
					for tick := 0; tick < 8; tick++ {
						e.ReconcileServiceDeployment(ctx, candidate.ID)
						current, err := store.DeploymentByID(ctx, candidate.ID)
						if err != nil {
							t.Fatal(err)
						}
						rows, err := store.ListInstancesForApp(ctx, app.ID)
						if err != nil {
							t.Fatal(err)
						}
						resident, serving := 0, 0
						for _, row := range rows {
							if state.State(row.State).CountsForRAM() {
								resident++
							}
							if row.State == "running" {
								serving++
							}
						}
						if resident > tc.limit+api.RolloutConcurrencyGrant || serving < tc.desired {
							t.Fatalf("tick %d: residents=%d serving=%d desired=%d limit=%d", tick, resident, serving, tc.desired, tc.limit)
						}
						if current.RolloutState == "complete" {
							break
						}
					}
					got, err := store.DeploymentByID(ctx, candidate.ID)
					if err != nil {
						t.Fatal(err)
					}
					rows, err := store.ListInstancesForApp(ctx, app.ID)
					if err != nil {
						t.Fatal(err)
					}
					oldRunning, newRunning, resident := 0, 0, 0
					for _, row := range rows {
						if state.State(row.State).CountsForRAM() {
							resident++
						}
						if row.State != "running" {
							continue
						}
						if row.DeploymentID == old.ID {
							oldRunning++
						}
						if row.DeploymentID == candidate.ID {
							newRunning++
						}
					}
					t.Logf("desired=%d max_concurrency=%d old_running=%d new_running=%d residents=%d candidate=%s/%s/%d", tc.desired, tc.limit, oldRunning, newRunning, resident, got.Status, got.RolloutState, got.TrafficPercent)
					if got.TrafficPercent != 100 || got.RolloutState != "complete" || oldRunning != 0 || newRunning != tc.desired || resident != tc.desired {
						t.Fatalf("rollout did not complete within its concurrency budget")
					}
				})
			}
		})
	}
}
