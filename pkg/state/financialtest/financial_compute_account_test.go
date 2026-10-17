package financialtest

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
)

type accountComputeFixture struct {
	store   computeTestStore
	account state.Account
	apps    []state.App
	owners  []runtimeadmission.Identity
	policy  state.FinancialBudget
}

func newAccountComputeFixture(t *testing.T, store computeTestStore, grant, limit int64, activate ...bool) accountComputeFixture {
	t.Helper()
	ctx := t.Context()
	f := accountComputeFixture{store: store, account: financialAccount(t, store)}
	for i := range 2 {
		app, err := store.CreateApp(ctx, state.App{AccountID: f.account.ID, Slug: fmt.Sprintf("ac-%s-%d", f.account.ID[:8], i), RAMMB: 128, MaxConcurrency: 20})
		if err != nil {
			t.Fatal(err)
		}
		f.apps = append(f.apps, app)
	}
	_, err := store.CreateComputeNode(ctx, state.ComputeNode{Name: uuid.NewString(), TargetURL: "unix:///tmp/account-compute.sock", VPCPUs: 8, MemMB: 8192, MaxConcurrency: 20, AdmissionCeilingMB: 7000, VCPUBudget: 8, Active: true, Lifecycle: state.NodeLifecycleActive})
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := store.ListComputeNodes(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes {
		owner := runtimeadmission.Identity{NodeID: n.ID, Incarnation: uuid.NewString(), ProtocolVersion: 1}
		if err := store.RegisterComputeNodeRuntimeIdentity(ctx, owner); err != nil {
			t.Fatal(err)
		}
		if err := store.RegisterFinancialComputeOwner(ctx, owner.NodeID, owner.Incarnation); err != nil {
			t.Fatal(err)
		}
		f.owners = append(f.owners, owner)
	}
	// These ledger fixtures run no physical guests. The current sampling
	// minute is empty too, so a test may cross its closing boundary without
	// manufacturing a missing-accounting failure during month recovery.
	emptyThrough := time.Now().UTC().Truncate(time.Minute)
	for at := f.account.CreatedAt.Truncate(time.Minute); !at.After(emptyThrough); at = at.Add(time.Minute) {
		if err := store.RecordFinancialSamplingWindow(ctx, at, true, false); err != nil {
			t.Fatal(err)
		}
	}
	f.price(t, grant, "initial")
	spec := financialBudgetSpec()
	spec.Mode = "strict"
	spec.Action = "suspend_workloads"
	spec.NotifyMillicents = []int64{}
	spec.LimitMillicents = limit
	if len(activate) > 0 && !activate[0] {
		f.policy = state.FinancialBudget{AccountID: f.account.ID, Spec: spec}
		return f
	}
	f.policy, err = store.CreateFinancialBudget(ctx, f.account.ID, uuid.NewString(), "test", spec)
	if err != nil {
		t.Fatalf("account activation: %v", err)
	}
	return f
}
func (f accountComputeFixture) price(t *testing.T, grant int64, version string) {
	t.Helper()
	month := financial.ComputeMonth(time.Now())
	_, err := f.store.PutFinancialPriceSnapshot(t.Context(), state.FinancialPriceSnapshot{AccountID: f.account.ID, PeriodStart: month, PeriodEnd: month.AddDate(0, 1, 0), Plan: api.PlanHobby, EffectiveFrom: month, DeliveryMode: "live", Price: financial.Price{Version: version, Meter: "compute", Currency: "USD", Unit: "mb_seconds", UnitQuantity: api.SecondsPerGBHour, MillicentsPerUnit: api.OverageMillicentsPerGBHour, IncludedQuantity: grant}})
	if err != nil {
		t.Fatal(err)
	}
}
func (f accountComputeFixture) request(i int) financial.ComputeRequest {
	owner := f.owners[i%len(f.owners)]
	return financial.ComputeRequest{AttemptID: uuid.NewString(), Fenced: true, AccountID: f.account.ID, AppID: f.apps[i%len(f.apps)].ID, InstanceID: uuid.NewString(), NodeID: owner.NodeID, Incarnation: owner.Incarnation, BillableMB: 1024}
}
func (f accountComputeFixture) balance(t *testing.T) financial.ComputeBalance {
	t.Helper()
	b, err := f.store.FinancialComputeBalance(t.Context(), f.policy, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func (f accountComputeFixture) settle(t *testing.T, l financial.ComputeLease) financial.ComputeLease {
	t.Helper()
	r := financial.ComputeRequest{LeaseID: l.ID, Fence: l.Fence, RequestID: uuid.NewString(), Revision: l.Revision, AccountID: l.AccountID, AppID: l.AppID, InstanceID: l.InstanceID, NodeID: l.NodeID, Incarnation: l.Incarnation}
	out, err := f.store.SettleFinancialCompute(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	again, err := f.store.SettleFinancialCompute(t.Context(), r)
	if err != nil || again.Revision != out.Revision {
		t.Fatalf("settlement replay: %+v %v", again, err)
	}
	return out
}
func TestFinancialComputeAccountReservations(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store computeTestStore = state.NewMemStore()
			if backend == "postgres" {
				store, _, _ = financialPostgres(t)
			}
			t.Run("cross_app_cross_node_one_pool", func(t *testing.T) {
				f := newAccountComputeFixture(t, store, 153600, 1)
				var mu sync.Mutex
				var winners []financial.ComputeLease
				var wg sync.WaitGroup
				for i := range 16 {
					wg.Go(func() {
						l, err := store.AcquireFinancialCompute(t.Context(), f.request(i))
						mu.Lock()
						defer mu.Unlock()
						if err == nil {
							winners = append(winners, l)
						} else if !errors.Is(err, financial.ErrComputeBudget) {
							t.Errorf("grant: %v", err)
						}
					})
				}
				wg.Wait()
				if len(winners) != 1 {
					t.Fatalf("winners=%d", len(winners))
				}
				l := winners[0]
				b := f.balance(t)
				if !l.AccountFunded || l.QuantityReserved != 153600 || b.AppID != "" || b.AccountID != f.account.ID || b.ScopeKind != "account" || b.Basis != "net_usage" || b.AllowanceReserved != 153600 || b.AllowanceRemaining != 0 || b.ReservedMillicents != 0 || !b.Held || b.UnconfirmedLeases != 1 {
					t.Fatalf("shared funding: lease=%+v balance=%+v", l, b)
				}
				// A durable account hold covers apps created after the original decision.
				app, err := store.CreateApp(t.Context(), state.App{AccountID: f.account.ID, Slug: "after-policy", RAMMB: 128, MaxConcurrency: 2})
				if err != nil {
					t.Fatal(err)
				}
				req := f.request(0)
				req.AppID = app.ID
				if _, err := store.AcquireFinancialCompute(t.Context(), req); !errors.Is(err, financial.ErrComputeBudget) {
					t.Fatalf("new app escaped: %v", err)
				}
				if after := f.balance(t); after.AllowanceReserved != b.AllowanceReserved || after.ReservedMillicents != b.ReservedMillicents {
					t.Fatalf("denial consumed funding: %+v", after)
				}
				if err := state.CheckFinancialBudgetAdmission(t.Context(), store, app, "", time.Now()); err == nil {
					t.Fatal("account hold did not reject traffic")
				}
				accountComputeHeldCommand(t, store, f.account.ID, app.ID)
				// Settlement reads the durable account book through a fresh transaction.
				f.settle(t, l)
				after := f.balance(t)
				if after.AllowanceReserved != 0 || after.UnconfirmedLeases != 0 || after.AllowanceCommitted == 0 || !after.Held {
					t.Fatalf("settlement: %+v", after)
				}
			})
			t.Run("app_denial_does_not_consume_account_allowance", func(t *testing.T) {
				f := newAccountComputeFixture(t, store, 153600, 1000)
				spec := f.policy.Spec
				spec.Scope = financial.BudgetScope{Kind: "app", ID: f.apps[0].ID}
				spec.Basis = "gross_usage"
				spec.LimitMillicents = 1
				p, err := store.CreateFinancialBudget(t.Context(), f.account.ID, uuid.NewString(), "test", spec)
				if err != nil {
					t.Fatal(err)
				}
				denied, err := store.AcquireFinancialCompute(t.Context(), f.request(0))
				if !errors.Is(err, financial.ErrComputeBudget) || denied.QuantityReserved != 0 || !denied.Denied {
					t.Fatalf("app denial: %+v %v", denied, err)
				}
				b := f.balance(t)
				if b.AllowanceReserved != 0 || b.ReservedMillicents != 0 || b.Held {
					t.Fatalf("app denial altered account: %+v", b)
				}
				appBalance, err := store.FinancialComputeBalance(t.Context(), p, time.Now())
				if err != nil || !appBalance.Held {
					t.Fatalf("app hold: %+v %v", appBalance, err)
				}
				l, err := store.AcquireFinancialCompute(t.Context(), f.request(1))
				if err != nil {
					t.Fatal(err)
				}
				if !l.AccountFunded || f.balance(t).AllowanceReserved != 153600 {
					t.Fatal("other app did not get shared allowance")
				}
				f.settle(t, l)
			})
			t.Run("grant_upgrade_downgrade_and_policy_replacement", func(t *testing.T) {
				f := newAccountComputeFixture(t, store, 1000, 1000)
				r := f.request(0)
				l, err := store.AcquireFinancialCompute(t.Context(), r)
				if err != nil {
					t.Fatal(err)
				}
				before := f.balance(t)
				if before.ReservedMillicents <= 0 || before.AllowanceReserved != 1000 {
					t.Fatalf("paid funding: %+v", before)
				}
				replay, err := store.AcquireFinancialCompute(t.Context(), r)
				if err != nil || replay.ID != l.ID || f.balance(t).ReservedMillicents != before.ReservedMillicents {
					t.Fatalf("acquire replay: %+v %v", replay, err)
				}
				f.price(t, 400000, "upgrade")
				f.price(t, 500, "downgrade")
				other, err := store.AcquireFinancialCompute(t.Context(), f.request(1))
				if err != nil {
					t.Fatal(err)
				}
				b := f.balance(t)
				if b.AllowanceGranted != 400000 || b.AllowanceReserved != 307200 || b.ReservedMillicents != 0 {
					t.Fatalf("grant was summed/revoked: %+v", b)
				}
				if _, err := store.DeleteFinancialBudget(t.Context(), f.account.ID, f.policy.ID, f.policy.Revision, "test"); err != nil {
					t.Fatal(err)
				}
				if b2 := f.balance(t); b2.AllowanceReserved != b.AllowanceReserved || b2.UnconfirmedLeases != 2 {
					t.Fatalf("delete freed funding: %+v", b2)
				}
				// Settlement must use persisted funding even when the issuing policy is gone.
				f.settle(t, l)
				f.settle(t, other)
				p, err := store.CreateFinancialBudget(t.Context(), f.account.ID, uuid.NewString(), "test", f.policy.Spec)
				if err != nil {
					t.Fatal(err)
				}
				f.policy = p
				after := f.balance(t)
				if after.AllowanceGranted != 400000 || after.AllowanceCommitted == 0 || after.AllowanceRemaining == 400000 {
					t.Fatalf("replacement reset usage: %+v", after)
				}
			})
			t.Run("job_definitions_and_queues_supported", func(t *testing.T) {
				f := newAccountComputeFixture(t, store, 0, 1000)
				_, err := store.JobCreate(t.Context(), f.account.ID, "supported", "batch", "example/image", []string{}, 128, 30, 1, 0, nil)
				if err != nil {
					t.Fatalf("job bypassed account cap: %v", err)
				}

				quota := store.(state.JobQuotaCreator)
				if _, err := quota.JobCreateIfUnderQuota(t.Context(), f.account.ID, "quota-blocked", "batch", "example/image", []string{}, 128, 30, 1, 0, nil, 10); err != nil {
					t.Fatalf("quota job bypass: %v", err)
				}
				scheduled := store.(state.JobScheduleCreateStore)
				if _, err := scheduled.JobCreateScheduledIfUnderQuota(t.Context(), state.Job{AccountID: f.account.ID, Name: "scheduled-blocked", Kind: "recurring", ImageRef: "example/image", Command: []string{}, RAMMB: 128, TaskTimeoutS: 30, MaxParallelism: 1, CronSchedule: "* * * * *", CronTimezone: "UTC"}, 10); err != nil {
					t.Fatalf("scheduled job bypass: %v", err)
				}
				image := store.(state.ImageRunJobStore)
				if _, err := image.JobEnsureImageRun(t.Context(), state.ImageRunEnsure{AccountID: f.account.ID, Name: "image-blocked", ImageRef: "example/image", RAMMB: 128, TaskTimeoutSec: 30, MaxCached: 10}); err != nil {
					t.Fatalf("image job bypass: %v", err)
				}
				if _, err := store.DeleteFinancialBudget(t.Context(), f.account.ID, f.policy.ID, f.policy.Revision, "test"); err != nil {
					t.Fatal(err)
				}
				_, err = store.JobCreate(t.Context(), f.account.ID, "after-disable", "batch", "example/image", []string{}, 128, 30, 1, 0, nil)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := store.CreateFinancialBudget(t.Context(), f.account.ID, uuid.NewString(), "test", f.policy.Spec); err != nil {
					t.Fatalf("account cannot activate over quiet jobs: %v", err)
				}
			})
			t.Run("historical_quantity_survives_deletion_and_corrections", func(t *testing.T) { accountComputeHistoricalQuantity(t, store) })
			t.Run("job_creation_races_activation", func(t *testing.T) { accountComputeJobActivationRace(t, store) })

		})
	}
}

func accountComputeHistoricalQuantity(t *testing.T, store computeTestStore) {
	t.Helper()
	ctx := t.Context()
	f := newAccountComputeFixture(t, store, 50000, 1000)
	if _, err := store.DeleteFinancialBudget(ctx, f.account.ID, f.policy.ID, f.policy.Revision, "test"); err != nil {
		t.Fatal(err)
	}
	month := financial.ComputeMonth(time.Now())
	if err := store.AppendUsage(ctx, f.account.ID, f.apps[0].ID, uuid.NewString(), month.Add(time.Hour), 100000, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	head, err := store.FinancialEvidenceHead(ctx, f.account.ID, month, month.AddDate(0, 1, 0))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := store.ListFinancialUsageEvidence(ctx, f.account.ID, month, month.AddDate(0, 1, 0), 0, head, 100)
	if err != nil || len(rows) != 1 {
		t.Fatalf("history: %+v %v", rows, err)
	}
	if _, err := store.AppendFinancialAdjustment(ctx, state.FinancialAdjustment{AccountID: f.account.ID, SourceID: "account-negative", CorrectsSourceID: rows[0].Evidence.SourceID, Quantity: -50000, Actor: "operator", Reason: "correction must not grant compute"}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteApp(ctx, f.apps[0].ID); err != nil {
		t.Fatal(err)
	}
	policy, err := store.CreateFinancialBudget(ctx, f.account.ID, uuid.NewString(), "test", f.policy.Spec)
	if err != nil {
		t.Fatal(err)
	}
	f.policy = policy
	balance := f.balance(t)
	expected, err := financial.ComputeOverageBound(100000, 50000, api.OverageMillicentsPerGBHour, api.SecondsPerGBHour)
	if err != nil {
		t.Fatal(err)
	}
	if balance.CommittedMillicents != expected || balance.AllowanceCommitted != 50000 || balance.AllowanceRemaining != 0 {
		t.Fatalf("history/correction minted allowance: %+v", balance)
	}
	spec := policy.Spec
	spec.LimitMillicents = expected - 1
	if _, err := store.UpdateFinancialBudget(ctx, f.account.ID, policy.ID, policy.Revision, "test", spec); !errors.Is(err, financial.ErrComputeBudget) {
		t.Fatalf("limit lost historical funding: %v", err)
	}
}

func accountComputeJobActivationRace(t *testing.T, store computeTestStore) {
	t.Helper()
	ctx := t.Context()
	f := newAccountComputeFixture(t, store, 0, 1000)
	if _, err := store.DeleteFinancialBudget(ctx, f.account.ID, f.policy.ID, f.policy.Revision, "test"); err != nil {
		t.Fatal(err)
	}
	gate := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		<-gate
		_, err := store.CreateFinancialBudget(ctx, f.account.ID, uuid.NewString(), "test", f.policy.Spec)
		results <- err
	}()
	go func() {
		<-gate
		_, err := store.JobCreate(ctx, f.account.ID, "raced-job", "batch", "example/image", []string{}, 128, 30, 1, 0, nil)
		results <- err
	}()
	close(gate)
	wins := 0
	for range 2 {
		err := <-results
		if err == nil {
			wins++
		} else if !errors.Is(err, financial.ErrComputeUnavailable) {
			t.Fatalf("race: %v", err)
		}
	}
	if wins != 2 {
		t.Fatalf("quiet job definition and account activation should both pass: winners=%d", wins)
	}
}

func accountComputeHeldCommand(t *testing.T, store computeTestStore, account, app string) {
	t.Helper()
	ctx := t.Context()
	commands := store.(state.AppTaskStore)
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app, Kind: state.DeploymentKindImage, ImageDigest: "sha256:account-command", Status: state.DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []state.DeploymentStatus{state.DeployBuilding, state.DeployImaging, state.DeploySnapshotting} {
		if err := store.UpdateDeploymentStatus(ctx, dep.ID, status, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SetDeploymentRootfs(ctx, dep.ID, "/tmp/account-command.ext4", "account/command.ext4", 4096); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	task, err := commands.CreateAppTask(ctx, state.CreateAppTaskParams{AccountID: account, AppID: app, DeploymentID: dep.ID, Kind: state.AppTaskKindManual, Command: []string{"true"}, TimeoutSeconds: 60, MaxOutputBytes: 1024, CreatedAt: now.Add(-time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := commands.ClaimNextAppTask(ctx, "scheduler", now, time.Minute); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("account-held command was claimed: %v", err)
	}
	kept, err := commands.AppTaskByID(ctx, account, app, task.ID)
	if err != nil || kept.Status != state.AppTaskQueued || kept.AttemptCount != 0 {
		t.Fatalf("held command consumed queue: %+v %v", kept, err)
	}
}
