package sched

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
	"github.com/onebox-faas/faas/pkg/tasktoolrehearsal"
)

type rehearsalReservationStore struct {
	MemStore           *state.MemStore
	account, run, plan string
	reservedAt         time.Time
}

func (s *rehearsalReservationStore) MatchHostedTaskRehearsalReservation(_ context.Context, account, run, plan string, reservedAt time.Time) (bool, error) {
	return account == s.account && run == s.run && plan == s.plan && reservedAt.Equal(s.reservedAt), nil
}

type rehearsalWorkerVM struct {
	*fakeVMM
	instance     string
	destroyCalls int
}

func (v *rehearsalWorkerVM) Destroy(ctx context.Context, node, instance string) error {
	v.destroyCalls++
	return v.fakeVMM.Destroy(ctx, node, instance)
}

func (v *rehearsalWorkerVM) Stats(context.Context, string) (*StatsSnapshot, error) {
	resident := int64(1)
	return &StatsSnapshot{Instances: []VMInstanceStat{{InstanceID: v.instance, ResidentBytes: &resident}}}, nil
}

func TestHostedTaskRehearsalInterruptsExactWorkerAtMostOnce(t *testing.T) {
	for _, destroyErr := range []error{nil, errors.New("vmmd reply lost")} {
		name := "confirmed"
		if destroyErr != nil {
			name = "uncertain"
		}
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			mem := state.NewMemStore()
			account, err := mem.CreateAccount(ctx, "rehearsal@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			run := uuid.New()
			scenario := strings.ReplaceAll(run.String(), "-", "")
			reservation := &rehearsalReservationStore{MemStore: mem, account: account.ID, run: run.String(),
				plan: strings.Repeat("a", 64), reservedAt: time.Now().Add(-time.Minute)}
			expiry := reservation.reservedAt.Add(tasktoolrehearsal.WorkloadLease)
			app, err := mem.CreateApp(ctx, state.App{ID: tasktoolrehearsal.WorkloadID(run, "worker"), AccountID: account.ID,
				Slug: "task-" + scenario[:24] + "-worker", PreviewOfSlug: "task-" + scenario,
				Status: state.AppActive, Visibility: api.AppVisibilityInternal, PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expiry})
			if err != nil {
				t.Fatal(err)
			}
			if err := mem.RegisterScenarioTestMembers(ctx, account.ID, scenario, []state.ScenarioTestMember{
				{AppID: app.ID, Workload: "worker", NetworkMode: state.ScenarioNetworkContained, WebhookMode: state.ScenarioWebhookCaptureMode},
			}); err != nil {
				t.Fatal(err)
			}
			dep, err := mem.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Scope: api.DefaultEnvScope})
			if err != nil {
				t.Fatal(err)
			}
			if err := mem.SetDeploymentRootfs(ctx, dep.ID, "/rootfs", "fixture/rootfs", 4096); err != nil {
				t.Fatal(err)
			}
			if err := mem.MarkDeploymentLive(ctx, dep.ID); err != nil {
				t.Fatal(err)
			}
			mem.SetDeploymentTrafficForTest(dep.ID, 0, true)
			node, err := mem.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
			if err != nil {
				t.Fatal(err)
			}
			ins, err := mem.CreateInstanceWithMode(ctx, app.ID, dep.ID, "running", 128, node.ID, uuid.NewString(), string(state.InstanceModeWorker))
			if err != nil {
				t.Fatal(err)
			}
			if err := mem.SetInstanceRuntime(ctx, ins.ID, "fixture", "127.0.0.1", 21001); err != nil {
				t.Fatal(err)
			}
			vm := &rehearsalWorkerVM{fakeVMM: &fakeVMM{destroyErr: destroyErr}, instance: ins.ID}
			e := newEngine(t, statetest.NewMem(reservation), vm, &fakeNotifier{}, "1.10.0")
			var reconcile string
			e.workerReconcileSubmit = func(_ context.Context, appID string) { reconcile = appID }
			in := tasktoolrehearsal.WorkerInterruption{AccountID: account.ID, RunID: run.String(), PlanSHA256: reservation.plan, TaskID: uuid.NewString(),
				AppID: app.ID, DeploymentID: dep.ID, InstanceID: ins.ID, NodeID: ins.NodeID, WakeID: ins.WakeID, ObservedAt: time.Now()}
			stale := in
			stale.ObservedAt = stale.ObservedAt.Add(-tasktoolrehearsal.WorkerStopDeadline)
			foreign := in
			foreign.WakeID = uuid.NewString()
			wrongPlan := in
			wrongPlan.PlanSHA256 = strings.Repeat("b", 64)
			for _, invalid := range []tasktoolrehearsal.WorkerInterruption{stale, foreign, wrongPlan} {
				if _, err := e.InterruptHostedTaskWorker(ctx, invalid); !errors.Is(err, state.ErrConflict) {
					t.Fatalf("invalid interruption returned %v", err)
				}
			}
			if vm.destroyCalls != 0 {
				t.Fatal("invalid interruption contacted vmmd")
			}
			in.ObservedAt = time.Now()
			stopped, err := e.InterruptHostedTaskWorker(ctx, in)
			expected := state.StateStopped
			if destroyErr != nil {
				expected = state.StateDraining
				if !errors.Is(err, destroyErr) || stopped != (tasktoolrehearsal.StoppedWorker{}) || reconcile != "" {
					t.Fatalf("uncertain destroy produced a receipt or replacement: %+v %v", stopped, err)
				}
			} else if err != nil || stopped.InstanceID != ins.ID || stopped.DeploymentID != dep.ID || reconcile != app.ID {
				t.Fatalf("confirmed destroy did not retire and replace the worker: %+v %v", stopped, err)
			}
			fresh, err := mem.InstanceByID(ctx, ins.ID)
			if err != nil || fresh.State != string(expected) {
				t.Fatalf("worker state = %q, want %s: %v", fresh.State, expected, err)
			}
			if _, err := e.InterruptHostedTaskWorker(ctx, in); !errors.Is(err, state.ErrConflict) || vm.destroyCalls != 1 {
				t.Fatalf("retry repeated destroy: calls=%d err=%v", vm.destroyCalls, err)
			}
		})
	}
}
