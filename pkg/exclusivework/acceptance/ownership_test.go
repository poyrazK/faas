package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/exclusivework"
	"github.com/onebox-faas/faas/pkg/state"
	"strings"
	"sync"
	"testing"
	"time"
)

type exclusiveFixture struct {
	store        state.ExclusiveWorkStore
	base         state.Store
	mem          *state.MemStore
	pool         *pgxpool.Pool
	account      string
	apps         []string
	incarnations []string
	deployments  []string
	now          time.Time
}

func newExclusiveFixture(t *testing.T, postgres bool) *exclusiveFixture {
	t.Helper()
	fx := &exclusiveFixture{now: time.Now().UTC()}
	if postgres {
		fx.pool = pgtest.OpenMigrated(t)
		if err := db.MigrateUp(context.Background(), fx.pool); err != nil {
			t.Fatal(err)
		}
		s := state.NewPgStore(fx.pool)
		fx.store, fx.base = s, s
	} else {
		s := state.NewMemStoreWithExclusiveClock(func() time.Time { return fx.now })
		fx.mem = s
		fx.store, fx.base = s, s
	}
	a, err := fx.base.CreateAccount(context.Background(), uuid.NewString()+"@exclusive.test", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	fx.account = a.ID
	var nodeID string
	if postgres {
		n, err := fx.base.(interface {
			ComputeNodeByName(context.Context, string) (state.ComputeNode, error)
		}).ComputeNodeByName(context.Background(), state.DefaultLocalNodeName)
		if err != nil {
			t.Fatal(err)
		}
		nodeID = n.ID
	} else {
		nodeID = uuid.NewString()
	}
	for range 2 {
		app, err := fx.base.CreateApp(context.Background(), state.App{AccountID: a.ID, Slug: "exclusive-" + uuid.NewString(), RAMMB: 256, Runtime: "node22"})
		if err != nil {
			t.Fatal(err)
		}
		fx.apps = append(fx.apps, app.ID)
		dep, err := fx.base.CreateDeployment(context.Background(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:exclusive", Status: state.DeployPending})
		if err != nil {
			t.Fatal(err)
		}
		if err = fx.base.MarkDeploymentLive(context.Background(), dep.ID); err != nil {
			t.Fatal(err)
		}
		instance, err := fx.base.CreateInstance(context.Background(), app.ID, dep.ID, string(state.StateRunning), 256, nodeID, uuid.NewString())
		if err != nil {
			t.Fatal(err)
		}
		fx.incarnations = append(fx.incarnations, state.ExclusiveIncarnation(instance))
		fx.deployments = append(fx.deployments, dep.ID)
	}
	return fx
}
func (fx *exclusiveFixture) incarnationForApp(appID string) string {
	for i, id := range fx.apps {
		if id == appID {
			return fx.incarnations[i]
		}
	}
	return ""
}
func (fx *exclusiveFixture) policy(t *testing.T, mode, scope string) state.ExclusiveWorkPolicy {
	t.Helper()
	p, err := fx.store.UpsertExclusiveWorkPolicy(context.Background(), fx.account, exclusivework.Policy{Name: "crm-sync", Scope: scope, Contention: mode, MemberAppIDs: fx.apps, LeaseSeconds: 5, MaxAttemptSeconds: 60})
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func (fx *exclusiveFixture) admit(app int, idem, equiv, tenant string) (state.ExclusiveOperation, bool, error) {
	return fx.store.AdmitExclusiveOperation(context.Background(), state.ExclusiveAdmission{AccountID: fx.account, AppID: fx.apps[app], PlatformTenantID: tenant, PolicyName: "crm-sync", Key: json.RawMessage(`"crm"`), Request: json.RawMessage(`{"method":"POST","path":"/sync","payload":{"version":42}}`), IdempotencyKey: idem, EquivalenceKey: equiv})
}
func (fx *exclusiveFixture) expire(t *testing.T, id string, deadline bool) {
	t.Helper()
	if fx.mem != nil {
		fx.now = fx.now.Add(6 * time.Second)
		if deadline {
			fx.now = fx.now.Add(time.Minute)
		}
		return
	}
	query := `update exclusive_work_operations set lease_expires_at=clock_timestamp()-interval '1 second' where id=$1`
	if deadline {
		query = `update exclusive_work_operations set lease_expires_at=clock_timestamp()-interval '1 second',attempt_deadline=clock_timestamp()-interval '1 second' where id=$1`
	}
	if _, err := fx.pool.Exec(context.Background(), query, id); err != nil {
		t.Fatal(err)
	}
}
func exclusiveBackends(t *testing.T, run func(*testing.T, *exclusiveFixture)) {
	t.Helper()
	for _, pg := range []bool{false, true} {
		name := "memory"
		if pg {
			name = "postgres"
		}
		t.Run(name, func(t *testing.T) { run(t, newExclusiveFixture(t, pg)) })
	}
}

func TestExclusiveOperationsCrossAppFIFOAndStaleOwner(t *testing.T) {
	exclusiveBackends(t, func(t *testing.T, fx *exclusiveFixture) {
		ctx := context.Background()
		fx.policy(t, "queue", "account")
		first, _, err := fx.admit(0, "request-a", "", "")
		if err != nil {
			t.Fatal(err)
		}
		second, _, err := fx.admit(1, "request-b", "", "")
		if err != nil {
			t.Fatal(err)
		}
		if first.KeyID != second.KeyID || second.Sequence != first.Sequence+1 {
			t.Fatalf("apps did not share lane: %+v %+v", first, second)
		}
		start := make(chan struct{})
		var wg sync.WaitGroup
		results := make(chan error, 2)
		var old exclusivework.Claim
		for _, id := range []string{first.ID, second.ID} {
			wg.Add(1)
			go func(id string) {
				defer wg.Done()
				<-start
				c, e := fx.store.ClaimExclusiveOperation(ctx, fx.account, id, fx.incarnationForApp(map[string]string{first.ID: fx.apps[0], second.ID: fx.apps[1]}[id]))
				if id == first.ID {
					old = c
				}
				results <- e
			}(id)
		}
		close(start)
		wg.Wait()
		close(results)
		success, busy := 0, 0
		for e := range results {
			if e == nil {
				success++
			} else if errors.Is(e, exclusivework.ErrBusy) {
				busy++
			} else {
				t.Fatal(e)
			}
		}
		if success != 1 || busy != 1 {
			t.Fatalf("claims success=%d busy=%d", success, busy)
		}
		fx.expire(t, first.ID, false)
		if _, e := fx.store.RenewExclusiveOperation(ctx, old); !errors.Is(e, exclusivework.ErrStaleOwner) {
			t.Fatalf("expired owner renewed: %v", e)
		}
		if e := fx.store.CommitExclusiveOperation(ctx, old, json.RawMessage(`{"stale":true}`), nil); !errors.Is(e, exclusivework.ErrStaleOwner) {
			t.Fatalf("expired owner committed: %v", e)
		}
		fresh, e := fx.store.ClaimExclusiveOperation(ctx, fx.account, first.ID, fx.incarnations[0])
		if e != nil {
			t.Fatal(e)
		}
		if fresh.Generation <= old.Generation || fresh.Token == old.Token {
			t.Fatal("replacement did not advance authority")
		}
		if e := fx.store.RetryExclusiveOperation(ctx, old, "stale release"); !errors.Is(e, exclusivework.ErrStaleOwner) {
			t.Fatalf("old owner released replacement: %v", e)
		}
		if _, e := fx.store.RenewExclusiveOperation(ctx, fresh); e != nil {
			t.Fatal(e)
		}
		effects := []exclusivework.Effect{{Name: "sync-completed", Payload: json.RawMessage(`{"customer":"acme"}`)}}
		if e := fx.store.CommitExclusiveOperation(ctx, fresh, json.RawMessage(`{"ok":true}`), effects); e != nil {
			t.Fatal(e)
		}
		next, e := fx.store.ClaimExclusiveOperation(ctx, fx.account, second.ID, fx.incarnations[1])
		if e != nil {
			t.Fatal(e)
		}
		if next.Generation <= fresh.Generation {
			t.Fatal("generation reset between distinct operations")
		}
		if e := fx.store.CommitExclusiveOperation(ctx, old, nil, effects); !errors.Is(e, exclusivework.ErrStaleOwner) {
			t.Fatalf("historic owner published effects: %v", e)
		}
		if fx.pool != nil {
			var count int
			if e := fx.pool.QueryRow(ctx, `select count(*) from exclusive_work_effects where operation_id=$1`, first.ID).Scan(&count); e != nil || count != 1 {
				t.Fatalf("effect count=%d error=%v", count, e)
			}
		}
		got, e := fx.store.ExclusiveOperationByID(ctx, fx.account, first.ID)
		if e != nil || got.State != "completed" {
			t.Fatalf("result=%+v error=%v", got, e)
		}
		encoded, _ := json.Marshal(got)
		if strings.Contains(string(encoded), fresh.Token) || strings.Contains(string(encoded), "claim_token") {
			t.Fatal("receipt exposed renewal authority")
		}
	})
}

func TestExclusivePolicyAcceptsAccountOwnedJobMembers(t *testing.T) {
	exclusiveBackends(t, func(t *testing.T, fx *exclusiveFixture) {
		ctx := context.Background()
		job, err := fx.base.JobCreate(ctx, fx.account, "nightly-import", "batch", "registry.example/importer:v1", []string{"/app/import"}, 256, 60, 1, 0, json.RawMessage(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		policy, err := fx.store.UpsertExclusiveWorkPolicy(ctx, fx.account, exclusivework.Policy{
			Name: "job-import", Scope: "account", MemberJobIDs: []string{job.ID}, Contention: "queue",
			LeaseSeconds: 5, MaxAttemptSeconds: 60,
		})
		if err != nil {
			t.Fatalf("upsert account-owned Job member: %v", err)
		}
		if len(policy.Policy.MemberJobIDs) != 1 || policy.Policy.MemberJobIDs[0] != job.ID {
			t.Fatalf("stored Job membership=%v", policy.Policy.MemberJobIDs)
		}

		foreign, err := fx.base.CreateAccount(ctx, uuid.NewString()+"@exclusive.test", api.PlanHobby)
		if err != nil {
			t.Fatal(err)
		}
		foreignJob, err := fx.base.JobCreate(ctx, foreign.ID, "foreign-import", "batch", "registry.example/importer:v1", []string{"/app/import"}, 256, 60, 1, 0, json.RawMessage(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fx.store.UpsertExclusiveWorkPolicy(ctx, fx.account, exclusivework.Policy{
			Name: "foreign-job", Scope: "account", MemberJobIDs: []string{foreignJob.ID}, Contention: "queue",
			LeaseSeconds: 5, MaxAttemptSeconds: 60,
		}); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("cross-account Job membership error=%v, want ErrNotFound", err)
		}
		if _, err := fx.store.UpsertExclusiveWorkPolicy(ctx, fx.account, exclusivework.Policy{
			Name: "environment-job", Scope: "account", EnvironmentID: uuid.NewString(), MemberJobIDs: []string{job.ID},
			Contention: "queue", LeaseSeconds: 5, MaxAttemptSeconds: 60,
		}); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("Job with app environment error=%v, want ErrInvalidArgument", err)
		}
		if _, err := fx.store.UpsertExclusiveWorkPolicy(ctx, fx.account, exclusivework.Policy{
			Name: "tenant-job", Scope: "platform_tenant", MemberJobIDs: []string{job.ID},
			Contention: "queue", LeaseSeconds: 5, MaxAttemptSeconds: 60,
		}); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("Job in tenant scope error=%v, want ErrInvalidArgument", err)
		}
	})
}

func TestExclusiveJobRunGenerationFencesStaleTaskResults(t *testing.T) {
	exclusiveBackends(t, func(t *testing.T, fx *exclusiveFixture) {
		ctx := context.Background()
		job, err := fx.base.JobCreate(ctx, fx.account, "managed-import", "batch", "registry.example/importer:v1", []string{"/app/import"}, 256, 60, 1, 0, json.RawMessage(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fx.store.UpsertExclusiveWorkPolicy(ctx, fx.account, exclusivework.Policy{
			Name: "managed-job", Scope: "account", MemberJobIDs: []string{job.ID}, Contention: "queue",
			LeaseSeconds: 5, MaxAttemptSeconds: 60,
		}); err != nil {
			t.Fatal(err)
		}
		operation, _, err := fx.store.AdmitExclusiveOperation(ctx, state.ExclusiveAdmission{
			AccountID: fx.account, JobID: job.ID, PolicyName: "managed-job",
			Key: json.RawMessage(`"customer:acme:import"`), Request: json.RawMessage(`{"kind":"job_run","run":{"tasks":1}}`),
		})
		if err != nil {
			t.Fatalf("admit Job operation: %v", err)
		}
		incarnation := "job-operation/" + operation.ID
		firstOwner, err := fx.store.ClaimExclusiveOperation(ctx, fx.account, operation.ID, incarnation)
		if err != nil {
			t.Fatalf("claim first generation: %v", err)
		}
		makeRun := func(ownerGeneration int64) state.JobRun {
			t.Helper()
			run, _, err := fx.base.JobRunCreate(ctx, job.ID, fx.account, "manual", nil, nil, nil,
				json.RawMessage(`{}`), 1, state.JobRunOptions{
					ID: uuid.NewString(), ExclusiveOperationID: operation.ID, ExclusiveGeneration: ownerGeneration,
				})
			if err != nil {
				t.Fatalf("create generation %d JobRun: %v", ownerGeneration, err)
			}
			return run
		}
		claimTask := func(run state.JobRun) (state.Instance, state.JobTask) {
			t.Helper()
			node, err := fx.base.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
			if err != nil {
				t.Fatalf("resolve local node: %v", err)
			}
			instanceID, leaseToken := uuid.NewString(), uuid.NewString()
			instance, err := fx.base.CreateAndClaimJobInstance(ctx, instanceID, job.ID, run.ID, 0,
				string(state.StateColdBooting), job.RAMMB, node.ID, uuid.NewString(), leaseToken,
				time.Now().UTC().Add(time.Minute), node.ID)
			if err != nil {
				t.Fatalf("claim task for generation %d: %v", run.ExclusiveGeneration, err)
			}
			task, err := fx.base.JobTaskGet(ctx, run.ID, 0)
			if err != nil {
				t.Fatal(err)
			}
			return instance, task
		}

		oldRun := makeRun(firstOwner.Generation)
		oldInstance, oldTask := claimTask(oldRun)
		fx.expire(t, operation.ID, false)
		newOwner, err := fx.store.ClaimExclusiveOperation(ctx, fx.account, operation.ID, incarnation)
		if err != nil || newOwner.Generation <= firstOwner.Generation {
			t.Fatalf("claim replacement generation=%d after %d: %v", newOwner.Generation, firstOwner.Generation, err)
		}
		newRun := makeRun(newOwner.Generation)
		newInstance, newTask := claimTask(newRun)

		staleErr := fx.base.JobTaskCompleteClaimedWithLogs(ctx, oldRun.ID, 0, oldInstance.ID,
			*oldTask.LeaseToken, "succeeded", 0, "", "", "obsolete output", false, time.Now().UTC())
		if staleErr == nil {
			t.Fatal("stale Job owner committed a task result after a replacement generation")
		}
		unchanged, err := fx.base.JobTaskGet(ctx, oldRun.ID, 0)
		if err != nil || unchanged.Status != "claimed" || unchanged.LogContent != "" {
			t.Fatalf("stale task result changed stored state: task=%+v err=%v", unchanged, err)
		}
		if err := fx.base.JobTaskCompleteClaimedWithLogs(ctx, newRun.ID, 0, newInstance.ID,
			*newTask.LeaseToken, "succeeded", 0, "", "", "current output", false, time.Now().UTC()); err != nil {
			t.Fatalf("current generation task result: %v", err)
		}
		settled, err := fx.base.JobRunRecompute(ctx, newRun.ID)
		if err != nil || settled.AggregateStatus != "succeeded" {
			t.Fatalf("settle managed JobRun: status=%q err=%v", settled.AggregateStatus, err)
		}
		if err := fx.store.CommitExclusiveOperation(ctx, newOwner, json.RawMessage(`{"job_run_id":"`+newRun.ID+`"}`), nil); err != nil {
			t.Fatalf("commit current Job operation: %v", err)
		}
		runs, err := fx.base.JobRunListByExclusiveOperation(ctx, fx.account, operation.ID)
		if err != nil || len(runs) != 2 {
			t.Fatalf("operation JobRun history=%d err=%v, want both generations", len(runs), err)
		}
	})
}

func TestExclusiveOperationsContentionAndIdempotency(t *testing.T) {
	for _, mode := range []string{"queue", "reject", "join_existing"} {
		t.Run(mode, func(t *testing.T) {
			exclusiveBackends(t, func(t *testing.T, fx *exclusiveFixture) {
				ctx := context.Background()
				fx.policy(t, mode, "account")
				first, _, err := fx.admit(0, "original", "equivalent", "")
				if err != nil {
					t.Fatal(err)
				}
				replay, _, err := fx.admit(0, "original", "equivalent", "")
				if err != nil || replay.ID != first.ID {
					t.Fatalf("idempotency replay %+v %v", replay, err)
				}
				second, joined, err := fx.admit(0, "second", "equivalent", "")
				switch mode {
				case "queue":
					if err != nil || joined || second.ID == first.ID {
						t.Fatalf("queue collapsed distinct work: %+v %v", second, err)
					}
				case "reject":
					if !errors.Is(err, exclusivework.ErrBusy) {
						t.Fatalf("reject admission: %v", err)
					}
				case "join_existing":
					if err != nil || !joined || second.ID != first.ID {
						t.Fatalf("join admission %+v %v", second, err)
					}
					if _, _, err = fx.admit(0, "third", "different", ""); !errors.Is(err, exclusivework.ErrBusy) {
						t.Fatalf("concurrency key treated as equivalence: %v", err)
					}
					if _, _, err = fx.admit(1, "other-app", "equivalent", ""); !errors.Is(err, exclusivework.ErrBusy) {
						t.Fatalf("different target joined: %v", err)
					}
				}
				claim, err := fx.store.ClaimExclusiveOperation(ctx, fx.account, first.ID, fx.incarnations[0])
				if err != nil {
					t.Fatal(err)
				}
				if err = fx.store.CommitExclusiveOperation(ctx, claim, json.RawMessage(`{"done":true}`), nil); err != nil {
					t.Fatal(err)
				}
				if mode == "join_existing" {
					replay, _, err = fx.admit(0, "second", "equivalent", "")
					if err != nil || replay.ID != first.ID {
						t.Fatalf("joined retry after completion enqueued new work: %+v %v", replay, err)
					}
				}
			})
		})
	}
}

func TestExclusiveTriggerBindingsResolveTrustedTriggerAndCustomerScope(t *testing.T) {
	exclusiveBackends(t, func(t *testing.T, fx *exclusiveFixture) {
		ctx := context.Background()
		fx.policy(t, "queue", "account")
		cron, err := fx.base.CreateCronWithOptions(ctx, fx.apps[0], "0 * * * *", "/sync", true, state.CronOptions{Timezone: "UTC"})
		if err != nil {
			t.Fatal(err)
		}
		webhooks, ok := fx.base.(state.InboundWebhookStore)
		if !ok {
			t.Fatal("inbound webhook store unavailable")
		}
		endpoint, err := webhooks.CreateInboundWebhookEndpointIfUnderQuota(ctx, state.InboundWebhookEndpoint{
			AppID: fx.apps[1], AccountID: fx.account, Name: "exclusive-webhook", Provider: state.InboundWebhookProviderStripe,
			TokenHash: []byte("0123456789abcdef0123456789abcdef"), SigningSecretSealed: []byte("sealed"), DeliveryPath: "/sync", Enabled: true,
		}, api.MustLimitsFor(api.PlanPro))
		if err != nil {
			t.Fatal(err)
		}
		bindings, ok := fx.base.(state.ExclusiveTriggerBindingStore)
		if !ok {
			t.Fatal("exclusive trigger binding store unavailable")
		}
		for _, input := range []state.ExclusiveTriggerBinding{
			{Source: "cron", TriggerID: cron.ID, AccountID: fx.account, PolicyName: "crm-sync", Key: json.RawMessage(`"customer:acme:crm-sync"`), EquivalenceKey: "sync"},
			{Source: "inbound_webhook", TriggerID: endpoint.ID, AccountID: fx.account, PolicyName: "crm-sync", Key: json.RawMessage(`"customer:acme:crm-sync"`), EquivalenceKey: "sync"},
		} {
			saved, err := bindings.UpsertExclusiveTriggerBinding(ctx, input)
			if err != nil {
				t.Fatalf("upsert %s binding: %v", input.Source, err)
			}
			if saved.AppID == "" || saved.AccountID != fx.account || saved.PolicyName != "crm-sync" {
				t.Fatalf("binding did not resolve trusted trigger scope: %+v", saved)
			}
			got, err := bindings.ExclusiveTriggerBinding(ctx, fx.account, input.Source, input.TriggerID)
			if err != nil || got.AppID != saved.AppID || string(got.Key) != string(input.Key) {
				t.Fatalf("binding read=%+v error=%v", got, err)
			}
		}
		broker, err := fx.base.CreateTriggerIfUnderQuota(ctx, fx.apps[0], "kafka", "exclusive-broker", true,
			[]byte(`{"topic":"crm"}`), "", 10, 1000, 3, 1<<20, "commit", api.MustLimitsFor(api.PlanPro))
		if err != nil {
			t.Fatal(err)
		}
		brokerBinding, err := bindings.UpsertExclusiveTriggerBinding(ctx, state.ExclusiveTriggerBinding{
			Source: "broker", TriggerID: broker.ID.String(), AccountID: fx.account, PolicyName: "crm-sync",
			Key: json.RawMessage(`"customer:acme:crm-sync"`), EquivalenceKey: "sync",
		})
		if err != nil || brokerBinding.AppID != fx.apps[0] {
			t.Fatalf("broker trigger binding=%+v err=%v", brokerBinding, err)
		}
		if _, err := bindings.ExclusiveTriggerBinding(ctx, fx.account, "broker", broker.ID.String()); err != nil {
			t.Fatalf("read broker trigger binding: %v", err)
		}
		if err := fx.base.DeleteTrigger(ctx, broker.ID.String(), fx.apps[0]); err != nil {
			t.Fatal(err)
		}
		if _, err := bindings.ExclusiveTriggerBinding(ctx, fx.account, "broker", broker.ID.String()); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("broker trigger deletion retained its binding: %v", err)
		}
		if _, err := bindings.UpsertExclusiveTriggerBinding(ctx, state.ExclusiveTriggerBinding{
			Source: "cron", TriggerID: cron.ID, AccountID: fx.account, PolicyName: "crm-sync",
			PlatformTenantID: uuid.NewString(), Key: json.RawMessage(`"spoofed-tenant"`),
		}); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("account-scoped binding accepted a supplied tenant id: %v", err)
		}
		if err := fx.base.DeleteCron(ctx, cron.ID, fx.apps[0]); err != nil {
			t.Fatal(err)
		}
		if _, err := bindings.ExclusiveTriggerBinding(ctx, fx.account, "cron", cron.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("cron deletion retained its binding: %v", err)
		}
		fireCron, err := fx.base.CreateCronWithOptions(ctx, fx.apps[0], "30 * * * *", "/sync", true, state.CronOptions{Timezone: "UTC"})
		if err != nil {
			t.Fatal(err)
		}
		operation, _, err := fx.admit(0, "fire-now-operation", "", "")
		if err != nil {
			t.Fatal(err)
		}
		requestID, err := fx.base.InsertFireNowRequest(ctx, fireCron.ID, fx.account)
		if err != nil {
			t.Fatal(err)
		}
		request, err := fx.base.ClaimPendingFireNowRequest(ctx)
		if err != nil || request.ID != requestID {
			t.Fatalf("claim fire-now row=%+v err=%v", request, err)
		}
		if err := fx.base.MarkFireNowRequestSucceeded(ctx, requestID, "", operation.ID); err != nil {
			t.Fatal(err)
		}
		finished, err := fx.base.GetFireNowRequest(ctx, requestID)
		if err != nil || finished.OperationID == nil || *finished.OperationID != operation.ID || finished.InvocationID != nil {
			t.Fatalf("fire-now operation receipt=%+v err=%v", finished, err)
		}
	})
}

func TestExclusiveTenantTriggerBindingRequiresActiveTenantAppLink(t *testing.T) {
	exclusiveBackends(t, func(t *testing.T, fx *exclusiveFixture) {
		ctx := context.Background()
		if _, err := fx.store.UpsertExclusiveWorkPolicy(ctx, fx.account, exclusivework.Policy{
			Name: "crm-sync", Scope: "platform_tenant", MemberAppIDs: fx.apps, Contention: "queue",
			LeaseSeconds: 5, MaxAttemptSeconds: 60,
		}); err != nil {
			t.Fatal(err)
		}
		cron, err := fx.base.CreateCronWithOptions(ctx, fx.apps[0], "0 * * * *", "/sync", true, state.CronOptions{Timezone: "UTC"})
		if err != nil {
			t.Fatal(err)
		}
		tenants := fx.base.(state.PlatformTenantStore)
		tenant, _, err := tenants.CreatePlatformTenant(ctx, fx.account, "acme", "Acme", 10)
		if err != nil {
			t.Fatal(err)
		}
		surface, err := fx.base.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{
			AccountID: fx.account, AppID: fx.apps[0], Name: "exclusive-tenant-app",
		}, api.MustLimitsFor(api.PlanPro))
		if err != nil {
			t.Fatal(err)
		}
		if err := fx.base.UpdateTenantSurfaceStatus(ctx, surface.ID, state.SurfaceStatusActive); err != nil {
			t.Fatal(err)
		}
		if _, err := tenants.LinkPlatformTenantSurface(ctx, fx.account, tenant.ID, surface.ID); err != nil {
			t.Fatal(err)
		}
		bindings := fx.base.(state.ExclusiveTriggerBindingStore)
		bound, err := bindings.UpsertExclusiveTriggerBinding(ctx, state.ExclusiveTriggerBinding{
			Source: "cron", TriggerID: cron.ID, AccountID: fx.account, PolicyName: "crm-sync",
			PlatformTenantID: tenant.ID, Key: json.RawMessage(`"crm-sync"`),
		})
		if err != nil || bound.PlatformTenantID != tenant.ID {
			t.Fatalf("active trusted tenant link rejected: binding=%+v err=%v", bound, err)
		}
		webhooks := fx.base.(state.InboundWebhookStore)
		endpoint, err := webhooks.CreateInboundWebhookEndpointIfUnderQuota(ctx, state.InboundWebhookEndpoint{
			AppID: fx.apps[1], AccountID: fx.account, Name: "unlinked-webhook", Provider: state.InboundWebhookProviderStripe,
			TokenHash: []byte("fedcba9876543210fedcba9876543210"), SigningSecretSealed: []byte("sealed"), DeliveryPath: "/sync", Enabled: true,
		}, api.MustLimitsFor(api.PlanPro))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := bindings.UpsertExclusiveTriggerBinding(ctx, state.ExclusiveTriggerBinding{
			Source: "inbound_webhook", TriggerID: endpoint.ID, AccountID: fx.account, PolicyName: "crm-sync",
			PlatformTenantID: tenant.ID, Key: json.RawMessage(`"crm-sync"`),
		}); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("unlinked app bound to tenant work: %v", err)
		}
		if _, err := tenants.SetPlatformTenantStatus(ctx, fx.account, tenant.ID, state.PlatformTenantSuspended); err != nil {
			t.Fatal(err)
		}
		if _, err := bindings.ExclusiveTriggerBinding(ctx, fx.account, "cron", cron.ID); !errors.Is(err, state.ErrPlatformTenantSuspended) {
			t.Fatalf("suspended tenant trigger remained dispatchable: %v", err)
		}
	})
}

func TestExclusiveOperationsTrustedTenantIsolation(t *testing.T) {
	exclusiveBackends(t, func(t *testing.T, fx *exclusiveFixture) {
		ctx := context.Background()
		fx.policy(t, "reject", "platform_tenant")
		tenants := fx.base.(state.PlatformTenantStore)
		a, _, err := tenants.CreatePlatformTenant(ctx, fx.account, "acme", "Acme", 10)
		if err != nil {
			t.Fatal(err)
		}
		b, _, err := tenants.CreatePlatformTenant(ctx, fx.account, "beta", "Beta", 10)
		if err != nil {
			t.Fatal(err)
		}
		first, _, err := fx.admit(0, "same-request", "", a.ID)
		if err != nil {
			t.Fatal(err)
		}
		other, _, err := fx.admit(0, "same-request", "", b.ID)
		if err != nil {
			t.Fatal(err)
		}
		if first.ID == other.ID || first.KeyID == other.KeyID {
			t.Fatal("tenant lanes or idempotency receipts collided")
		}
		if _, _, err = fx.admit(0, "no-tenant", "", ""); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("missing trusted scope accepted: %v", err)
		}
		if _, _, err = fx.admit(0, "forged-tenant", "", uuid.NewString()); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("forged tenant accepted: %v", err)
		}
		if _, err = tenants.SetPlatformTenantStatus(ctx, fx.account, a.ID, state.PlatformTenantSuspended); err != nil {
			t.Fatal(err)
		}
		if _, err = fx.store.ClaimExclusiveOperation(ctx, fx.account, first.ID, fx.incarnations[0]); !errors.Is(err, state.ErrPlatformTenantSuspended) {
			t.Fatalf("suspended customer claimed work: %v", err)
		}
	})
}

func TestExclusiveOperationsDeadlineAndCancellation(t *testing.T) {
	exclusiveBackends(t, func(t *testing.T, fx *exclusiveFixture) {
		ctx := context.Background()
		fx.policy(t, "queue", "account")
		first, _, err := fx.admit(0, "a", "", "")
		if err != nil {
			t.Fatal(err)
		}
		second, _, err := fx.admit(1, "b", "", "")
		if err != nil {
			t.Fatal(err)
		}
		old, err := fx.store.ClaimExclusiveOperation(ctx, fx.account, first.ID, fx.incarnations[0])
		if err != nil {
			t.Fatal(err)
		}
		fx.expire(t, first.ID, true)
		fresh, err := fx.store.ClaimExclusiveOperation(ctx, fx.account, second.ID, fx.incarnations[1])
		if err != nil {
			t.Fatal(err)
		}
		if err = fx.store.CancelExclusiveOperation(ctx, fx.account, second.ID); err != nil {
			t.Fatal(err)
		}
		for _, c := range []exclusivework.Claim{old, fresh} {
			if err = fx.store.CommitExclusiveOperation(ctx, c, nil, nil); !errors.Is(err, exclusivework.ErrStaleOwner) {
				t.Fatalf("cancelled/dead owner committed: %v", err)
			}
		}
		got, err := fx.store.ExclusiveOperationByID(ctx, fx.account, first.ID)
		if err != nil || got.State != "failed" {
			t.Fatalf("deadline state=%s err=%v", got.State, err)
		}
	})
}

func TestExclusiveOperationFencesSnapshotRestore(t *testing.T) {
	exclusiveBackends(t, func(t *testing.T, fx *exclusiveFixture) {
		ctx := context.Background()
		fx.policy(t, "queue", "account")
		op, _, err := fx.admit(0, "restore", "", "")
		if err != nil {
			t.Fatal(err)
		}
		old, err := fx.store.ClaimExclusiveOperation(ctx, fx.account, op.ID, fx.incarnations[0])
		if err != nil {
			t.Fatal(err)
		}
		barrier := fx.base.(state.ExclusiveSnapshotStore)
		if err = barrier.BeginExclusiveSnapshot(ctx, strings.Split(old.IncarnationID, "/")[0]); !errors.Is(err, exclusivework.ErrBusy) {
			t.Fatalf("snapshot barrier accepted active owner: %v", err)
		}
		instanceID := strings.Split(old.IncarnationID, "/")[0]
		if err = fx.base.UpdateInstanceStateWithTimestamp(ctx, instanceID, string(state.StateSnapshotting), time.Now()); err == nil {
			t.Fatal("direct snapshot transition bypassed active lease guard")
		}
		previous, err := fx.base.InstanceByID(ctx, instanceID)
		if err != nil || previous.State != string(state.StateRunning) {
			t.Fatalf("guard changed runtime state: %+v %v", previous, err)
		}
		if err = fx.base.(interface {
			FailRunningInstanceIfOwnedByNode(context.Context, string, string, time.Time) error
		}).FailRunningInstanceIfOwnedByNode(ctx, instanceID, previous.NodeID, time.Now()); err != nil {
			t.Fatal(err)
		}
		if _, err = fx.store.ValidateExclusiveOperation(ctx, old); !errors.Is(err, exclusivework.ErrStaleOwner) {
			t.Fatalf("stopped incarnation retained ownership: %v", err)
		}
		freshInstance, err := fx.base.CreateInstance(ctx, fx.apps[0], fx.deployments[0], string(state.StateRunning), 256, previous.NodeID, uuid.NewString())
		if err != nil {
			t.Fatal(err)
		}
		fresh, err := fx.store.ClaimExclusiveOperation(ctx, fx.account, op.ID, state.ExclusiveIncarnation(freshInstance))
		if err != nil {
			t.Fatal(err)
		}
		if fresh.Generation <= old.Generation {
			t.Fatalf("restored runtime reused authority generation: old=%d new=%d", old.Generation, fresh.Generation)
		}
		if err = fx.store.CommitExclusiveOperation(ctx, old, json.RawMessage(`{"stale":true}`), nil); !errors.Is(err, exclusivework.ErrStaleOwner) {
			t.Fatalf("old snapshot authority committed: %v", err)
		}
		if err = fx.store.CommitExclusiveOperation(ctx, fresh, json.RawMessage(`{"restored":true}`), nil); err != nil {
			t.Fatal(err)
		}
	})
}
