package state

// adr: 617
import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

type quotaWriteFixture struct {
	store      Store
	quota      regionalQuotaFixture
	account    Account
	deployment Deployment
	node       ComputeNode
}

func newQuotaWriteFixture(t *testing.T, s Store, account Account, cell string) quotaWriteFixture {
	t.Helper()
	f := newRegionalQuotaFixture(t, s, s.(RegionalRuntimeQuotaStore), account, cell)
	d, err := s.CreateDeployment(t.Context(), Deployment{AppID: f.grant.Binding.RegionalAppID, Kind: DeploymentKindImage, ImageDigest: "sha256:quota-write-fence", Status: DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.MarkDeploymentLive(t.Context(), d.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.SetDeploymentRootfs(t.Context(), d.ID, "/tmp/quota-write.ext4", "apps/quota-write/rootfs.ext4", 4096); err != nil {
		t.Fatal(err)
	}
	region := "eu-fsn"
	node, err := s.CreateComputeNode(t.Context(), ComputeNode{Name: "quota-" + uuid.NewString(), Region: &region, Active: true,
		TargetURL: "unix:///tmp/quota-write-vmmd.sock", AdmissionCeilingMB: 4096, MemMB: 8192, VPCPUs: 4, VCPUBudget: 8, MaxConcurrency: 32})
	if err != nil {
		t.Fatal(err)
	}
	return quotaWriteFixture{s, f, account, d, node}
}

func (f quotaWriteFixture) freeze(t *testing.T) {
	t.Helper()
	if _, err := f.quota.authority.AcceptGrant(t.Context(), f.quota.grantEnvelope(t, f.quota.grant)); err != nil {
		t.Fatal(err)
	}
}

func (f quotaWriteFixture) instance(t *testing.T, state State) Instance {
	t.Helper()
	i, err := f.store.CreateInstance(t.Context(), f.quota.grant.Binding.RegionalAppID, f.deployment.ID, string(state), 128, f.node.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	return i
}

func (f quotaWriteFixture) task(t *testing.T, created time.Time, deadline *time.Time) AppTask {
	t.Helper()
	task, err := f.store.CreateAppTask(t.Context(), CreateAppTaskParams{
		AccountID: f.account.ID, AppID: f.quota.grant.Binding.RegionalAppID, DeploymentID: f.deployment.ID,
		Kind: AppTaskKindManual, Command: []string{"bin/check --quota"}, CommandShell: true,
		CreatedAt: created, StartDeadlineAt: deadline, RetryBackoffSeconds: 3,
		FailureRules: &workpolicy.FailureRules{Version: 1, UnmatchedFailure: "retry", UncertainOutcome: "hold"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func runRegionalQuotaWriteContract(t *testing.T, open func(*testing.T) quotaWriteFixture) {
	t.Helper()
	t.Run("all-resident-creations-refused", func(t *testing.T) {
		f := open(t)
		f.freeze(t)
		for _, state := range []State{StateWaking, StateColdBooting, StateRunning, StateDraining, StateSnapshotting, StateMigrating, StateWarm} {
			_, err := f.store.CreateInstance(t.Context(), f.quota.grant.Binding.RegionalAppID, f.deployment.ID, string(state), 128, f.node.ID, uuid.NewString())
			if !errors.Is(err, ErrRegionalRuntimeQuotaFrozen) {
				t.Fatalf("create %s: %v", state, err)
			}
		}
		for _, mode := range []InstanceMode{InstanceModeNormal, InstanceModeMirror} {
			_, err := f.store.CreateInstanceWithMode(t.Context(), f.quota.grant.Binding.RegionalAppID, f.deployment.ID, string(StateWarm), 128, f.node.ID, uuid.NewString(), string(mode))
			if !errors.Is(err, ErrRegionalRuntimeQuotaFrozen) {
				t.Fatalf("mode %s: %v", mode, err)
			}
		}
		rows, err := f.store.ListInstancesForApp(t.Context(), f.quota.grant.Binding.RegionalAppID)
		if err != nil || len(rows) != 0 {
			t.Fatalf("refusal left rows: %+v %v", rows, err)
		}
		// History and teardown are allowed, but resurrecting history is not.
		i := f.instance(t, StateParked)
		if err = f.store.UpdateInstanceState(t.Context(), i.ID, string(StateWaking)); !errors.Is(err, ErrRegionalRuntimeQuotaFrozen) {
			t.Fatalf("resurrect: %v", err)
		}
		if err = f.store.UpdateInstanceStateToTerminal(t.Context(), i.ID, string(StateStopped), time.Now()); err != nil {
			t.Fatal(err)
		}
		if err = f.store.DeleteInstance(t.Context(), i.ID); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("late-publication-and-warm-resume-refused", func(t *testing.T) {
		f := open(t)
		boot, warm, source := f.instance(t, StateColdBooting), f.instance(t, StateWarm), f.instance(t, StateRunning)
		f.freeze(t)
		for name, write := range map[string]func() error{
			"mode": func() error { return f.store.SetInstanceMode(t.Context(), boot.ID, InstanceModeMirror) },
			"runtime": func() error {
				return f.store.SetInstanceRuntime(t.Context(), boot.ID, "quota-netns", "192.0.2.5", 20001)
			},
			"publication": func() error {
				_, err := f.store.PublishInstanceRuntime(t.Context(), boot.ID, boot.State, "quota-netns", "192.0.2.5", 20001)
				return err
			},
			"warm-resume": func() error {
				return f.store.UpdateInstanceStateIf(t.Context(), warm.ID, warm.State, string(StateRunning))
			},
		} {
			if err := write(); !errors.Is(err, ErrRegionalRuntimeQuotaFrozen) {
				t.Fatalf("%s: %v", name, err)
			}
		}
		for _, before := range []Instance{boot, warm, source} {
			after, err := f.store.InstanceByID(t.Context(), before.ID)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("refusal changed instance: before=%+v after=%+v %v", before, after, err)
			}
		}
		// Pure pause, quiesce and cleanup never reopen execution.
		if err := f.store.UpdateInstanceState(t.Context(), source.ID, string(StateWarm)); err != nil {
			t.Fatal(err)
		}
		if err := f.store.UpdateInstanceState(t.Context(), source.ID, string(StateSnapshotting)); err != nil {
			t.Fatal(err)
		}
		if err := f.store.UpdateInstanceState(t.Context(), source.ID, string(StateWarm)); !errors.Is(err, ErrRegionalRuntimeQuotaFrozen) {
			t.Fatalf("snapshot-to-warm: %v", err)
		}
		if err := f.store.UpdateInstanceState(t.Context(), boot.ID, boot.State); err != nil {
			t.Fatal(err)
		}
		for _, i := range []Instance{boot, warm, source} {
			if err := f.store.UpdateInstanceStateToTerminal(t.Context(), i.ID, string(StateFailed), time.Now()); err != nil {
				t.Fatal(err)
			}
		}
	})
	t.Run("migration-source-retained", func(t *testing.T) {
		f := open(t)
		i := f.instance(t, StateRunning)
		lease := uuid.NewString()
		f.freeze(t)
		if err := f.store.MarkInstanceMigrating(t.Context(), i.ID, f.node.ID, lease); err != nil {
			t.Fatal(err)
		}
		if err := f.store.MigrateInstanceOwner(t.Context(), i.ID, f.node.ID, f.node.ID, lease); !errors.Is(err, ErrRegionalRuntimeQuotaFrozen) {
			t.Fatalf("migration publication: %v", err)
		}
		if err := f.store.ReinviteMigratingInstance(t.Context(), i.ID, lease); !errors.Is(err, ErrRegionalRuntimeQuotaFrozen) {
			t.Fatalf("migration source resume: %v", err)
		}
		after, err := f.store.InstanceByID(t.Context(), i.ID)
		if err != nil || after.State != string(StateMigrating) || after.NodeID != f.node.ID {
			t.Fatalf("source lost: %+v %v", after, err)
		}
		if err = f.store.CancelInstanceMigration(t.Context(), i.ID, f.node.ID, lease); err != nil {
			t.Fatal(err)
		}
		if err = f.store.UpdateInstanceState(t.Context(), i.ID, string(StateRunning)); !errors.Is(err, ErrRegionalRuntimeQuotaFrozen) {
			t.Fatalf("rollback resume: %v", err)
		}
	})
	t.Run("task-claim-skips-frozen-and-cleans-expired", func(t *testing.T) {
		f := open(t)
		base := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
		queued := f.task(t, base, nil)
		deadline := base.Add(time.Second)
		expired := f.task(t, base, &deadline)
		f.freeze(t)
		if _, err := f.store.ClaimNextAppTask(t.Context(), "quota-worker", base.Add(2*time.Second), time.Minute); !errors.Is(err, ErrNotFound) {
			t.Fatalf("frozen claim: %v", err)
		}
		got, err := f.store.AppTaskByID(t.Context(), f.account.ID, queued.AppID, queued.ID)
		if err != nil || !reflect.DeepEqual(got, queued) {
			t.Fatalf("queued intent changed: %+v %v", got, err)
		}
		got, err = f.store.AppTaskByID(t.Context(), f.account.ID, expired.AppID, expired.ID)
		if err != nil || got.Status != AppTaskCancelled {
			t.Fatalf("deadline cleanup: %+v %v", got, err)
		}
		independent := newQuotaWriteFixture(t, f.store, f.account, uuid.NewString())
		ready := independent.task(t, base.Add(3*time.Second), nil)
		claim, err := f.store.ClaimNextAppTask(t.Context(), "quota-worker", base.Add(4*time.Second), time.Minute)
		if err != nil || claim.ID != ready.ID || claim.Status != AppTaskRestoring || claim.LeaseToken == nil {
			t.Fatalf("independent task starved: %+v %v", claim, err)
		}
		stored, err := f.store.AppTaskByID(t.Context(), f.account.ID, ready.AppID, ready.ID)
		if err != nil || !reflect.DeepEqual(claim, stored) {
			t.Fatalf("claim lost pins: claim=%+v stored=%+v %v", claim, stored, err)
		}
	})
	t.Run("late-task-start-refused-cleanup-allowed", func(t *testing.T) {
		f := open(t)
		base := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
		intent := f.task(t, base, nil)
		claim, err := f.store.ClaimNextAppTask(t.Context(), "quota-worker", base.Add(time.Second), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		f.freeze(t)
		if _, err = f.store.MarkAppTaskRunning(t.Context(), intent.ID, *claim.LeaseToken, base.Add(2*time.Second)); !errors.Is(err, ErrRegionalRuntimeQuotaFrozen) {
			t.Fatalf("late task start: %v", err)
		}
		stored, err := f.store.AppTaskByID(t.Context(), f.account.ID, intent.AppID, intent.ID)
		if err != nil || !reflect.DeepEqual(claim, stored) {
			t.Fatalf("refusal changed task: %+v %v", stored, err)
		}
		if err = f.store.RenewAppTaskLease(t.Context(), intent.ID, *claim.LeaseToken, base.Add(3*time.Second), time.Minute); err != nil {
			t.Fatal(err)
		}
		if _, err = f.store.RequestAppTaskCancellation(t.Context(), f.account.ID, intent.AppID, intent.ID, base.Add(4*time.Second)); err != nil {
			t.Fatal(err)
		}
		terminal, err := f.store.CompleteAppTask(t.Context(), CompleteAppTaskParams{ID: intent.ID, LeaseToken: *claim.LeaseToken, Status: AppTaskCancelled, FinishedAt: base.Add(5 * time.Second)})
		if err != nil || terminal.Status != AppTaskCancelled {
			t.Fatalf("cleanup: %+v %v", terminal, err)
		}
	})
}

func TestMemRegionalQuotaWriteFences(t *testing.T) {
	runRegionalQuotaWriteContract(t, func(t *testing.T) quotaWriteFixture {
		s := NewMemStore()
		a, err := s.CreateAccount(t.Context(), "write-fence@example.com", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		return newQuotaWriteFixture(t, s, a, uuid.NewString())
	})
}

func TestMemRegionalQuotaWriteFenceUppercase(t *testing.T) {
	s := NewMemStore()
	a, err := s.CreateAccount(t.Context(), "uppercase-fence@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	f := newQuotaWriteFixture(t, s, a, uuid.NewString())
	f.freeze(t)
	if _, err = s.CreateInstanceWithMode(t.Context(), f.quota.grant.Binding.RegionalAppID, f.deployment.ID, "RUNNING", 128, f.node.ID, uuid.NewString(), string(InstanceModeMirror)); !errors.Is(err, ErrRegionalRuntimeQuotaFrozen) {
		t.Fatalf("uppercase admission: %v", err)
	}
}
