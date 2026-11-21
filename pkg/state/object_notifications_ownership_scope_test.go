package state_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 552
func TestObjectNotificationOwnershipAndScopeMem(t *testing.T) {
	st := state.NewMemStore()
	objectNotificationOwnershipAndScope(t, st, func(t *testing.T, app state.App, count int) {
		for range count {
			if _, err := st.EnqueueInvocation(t.Context(), state.Invocation{AppID: app.ID, AccountID: app.AccountID, Source: state.InvocationQueue, QueueName: "filler", Payload: json.RawMessage(`{}`), DueAt: time.Now()}); err != nil {
				t.Fatal(err)
			}
		}
	})
}

func TestObjectNotificationOwnershipAndScopePG(t *testing.T) {
	st, _, pool := pgWithPool(t)
	objectNotificationOwnershipAndScope(t, st, func(t *testing.T, app state.App, count int) {
		if _, err := pool.Exec(t.Context(), `INSERT INTO invocations (app_id,account_id,source,queue_name,deployment_scope)
SELECT $1,$2,'queue','filler','default' FROM generate_series(1,$3)`, app.ID, app.AccountID, count); err != nil {
			t.Fatal(err)
		}
	})
}

func objectNotificationOwnershipAndScope(t *testing.T, st accountingStore, fill func(*testing.T, state.App, int)) {
	for _, tc := range []struct {
		name          string
		personal, org api.Plan
		allowed       bool
	}{
		{"paid-company-free-creator", api.PlanFree, api.PlanPro, true},
		{"free-company-paid-creator", api.PlanPro, api.PlanFree, false},
		{"paid-control", api.PlanPro, api.PlanPro, true},
		{"free-control", api.PlanFree, api.PlanFree, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			full := st.(state.Store)
			ctx := t.Context()
			account, err := st.CreateAccount(ctx, uuid.NewString()+"@example.test", tc.personal)
			if err != nil {
				t.Fatal(err)
			}
			org, err := full.CreateOrg(ctx, state.Org{Slug: "notify-" + uuid.NewString()[:8], Name: "Notifications", Plan: tc.org})
			if err != nil {
				t.Fatal(err)
			}
			// Activate paid company ownership through the billing adoption seams.
			if err = full.UpdateAccountProviderCustomerID(ctx, org.ID, "cus_"+org.ID); err != nil {
				t.Fatal(err)
			}
			if err = full.UpdateAccountStripeSubscriptionItem(ctx, org.ID, "si_"+org.ID); err != nil {
				t.Fatal(err)
			}
			if err = full.AddOrgMember(ctx, org.ID, account.ID, state.OrgRoleOwner, nil); err != nil {
				t.Fatal(err)
			}
			app, err := full.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: account.ID, OrgID: org.ID, Slug: "notify-" + uuid.NewString(), Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 1})
			if err != nil {
				t.Fatal(err)
			}
			owner, err := state.AppResourceAccount(ctx, full, app)
			if err != nil || owner.ResourceOwnerID() != org.ID || owner.Plan != tc.org {
				t.Fatal("company ownership control", owner, err)
			}
			bucket := notificationBucketForTest(t, st, app)
			binding, err := full.CreateQueueBinding(ctx, state.QueueBinding{AccountID: account.ID, AppID: app.ID, Name: "images", QueueName: "images", Mode: "pull", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 1})
			if err != nil {
				t.Fatal(err)
			}
			notifications := st.(state.ObjectNotificationStore)
			for _, kind := range []string{"function", "queue"} {
				t.Run(kind, func(t *testing.T) {
					rule := notificationRuleForTest(bucket, app, kind)
					_, configErr := notifications.SetObjectBucketNotifications(t.Context(), account.ID, app.ID, bucket.ID, []api.ObjectNotificationRule{rule})
					inv := notificationInvocationForTest(app, kind)
					snapshot := state.ObjectNotificationSnapshot{Rule: rule, BucketName: bucket.Name, BucketRegion: bucket.Region}
					if kind == "queue" {
						snapshot.QueueBindingID, snapshot.RetryPolicy = binding.ID, json.RawMessage(`{}`)
					}
					enqueueErr := notifications.EnqueueObjectNotification(t.Context(), inv, snapshot)
					if !tc.allowed {
						if !errors.Is(configErr, state.ErrObjectNotificationInvalid) || !errors.Is(enqueueErr, state.ErrObjectNotificationCapacity) {
							t.Fatalf("free company used paid creator's plan: config=%v delivery=%v", configErr, enqueueErr)
						}
						return
					}
					if configErr != nil || enqueueErr != nil {
						t.Fatalf("paid company blocked by creator's plan: config=%v delivery=%v", configErr, enqueueErr)
					}
					if kind == "queue" && tc.personal == api.PlanPro {
						if err := full.UpdateOrgPlan(t.Context(), org.ID, api.PlanFree); err != nil {
							t.Fatal(err)
						}
						if err := notifications.EnqueueObjectNotification(t.Context(), inv, snapshot); err != nil {
							t.Fatal("committed replay blocked by company downgrade", err)
						}
						inv.ID = uuid.NewString()
						if err := notifications.EnqueueObjectNotification(t.Context(), inv, snapshot); !errors.Is(err, state.ErrObjectNotificationCapacity) {
							t.Fatal("delivery ignored company downgrade", err)
						}
						if _, err := notifications.SetObjectBucketNotifications(t.Context(), account.ID, app.ID, bucket.ID, []api.ObjectNotificationRule{rule}); !errors.Is(err, state.ErrObjectNotificationInvalid) {
							t.Fatal("configuration ignored company downgrade", err)
						}
					}
					if kind != "queue" || tc.personal != api.PlanFree {
						return
					}
					// A personal upgrade must not increase the company's queue ceiling.
					if err := full.UpdateAccountPlan(t.Context(), account.ID, api.PlanScale); err != nil {
						t.Fatal(err)
					}
					if err := full.UpdateOrgPlan(t.Context(), org.ID, api.PlanHobby); err != nil {
						t.Fatal(err)
					}
					fill(t, app, api.MustLimitsFor(api.PlanHobby).MaxQueueDepth-1)
					inv.ID = uuid.NewString()
					if err := notifications.EnqueueObjectNotification(t.Context(), inv, snapshot); !errors.Is(err, state.ErrObjectNotificationCapacity) {
						t.Fatal("personal upgrade raised company queue capacity", err)
					}
				})
			}
		})
	}
	t.Run("captured-scoped-queue", func(t *testing.T) { objectNotificationCapturedScope(t, st) })
}

// Plan admission needs a ready bucket without a provider usage report. Company
// usage reports belong to the adopted billing owner rather than the creator.
func notificationBucketForTest(t *testing.T, st accountingStore, app state.App) state.ObjectBucket {
	t.Helper()
	ctx := t.Context()
	bucket, err := st.ReserveObjectBucket(ctx, state.ObjectBucket{ID: uuid.NewString(), AccountID: app.AccountID, AppID: app.ID, Name: "assets", Scope: "default", Region: "us-east-1", BackendID: "external", BackendFingerprint: strings.Repeat("a", 64), PhysicalName: "gregale-" + uuid.NewString()}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.ClaimObjectBucket(ctx, app.AccountID, app.ID, bucket.ID, "create", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err = st.FinishObjectBucket(ctx, bucket.ID, "create", "ready"); err != nil {
		t.Fatal(err)
	}
	return bucket
}

func notificationRuleForTest(bucket state.ObjectBucket, app state.App, kind string) api.ObjectNotificationRule {
	arn := "arn:gregale:lambda:" + bucket.Region + ":" + uuid.MustParse(app.AccountID).String() + ":function:" + app.ID
	if kind == "queue" {
		arn = "arn:gregale:sqs:" + bucket.Region + ":" + uuid.MustParse(app.AccountID).String() + ":" + app.ID + "/images"
	}
	return api.ObjectNotificationRule{ID: "on-put", Destination: arn, Events: []string{"s3:ObjectCreated:Put"}}
}

func notificationInvocationForTest(app state.App, kind string) state.Invocation {
	in := state.Invocation{ID: uuid.NewString(), AppID: app.ID, AccountID: app.AccountID, Source: state.InvocationAsyncInvoke, Method: "POST", Path: "/", Payload: json.RawMessage(`{"Records":[]}`), Headers: json.RawMessage(`{}`), DueAt: time.Now().Add(-time.Second)}
	if kind == "queue" {
		in.Source, in.QueueName = state.InvocationQueue, "images"
	}
	return in
}

func objectNotificationCapturedScope(t *testing.T, st accountingStore) {
	ctx := t.Context()
	full := st.(state.Store)
	account, err := st.CreateAccount(ctx, uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := full.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "queues-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	staging, err := full.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := full.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: account.ID, ProjectID: project.ID, WorkloadName: "worker", Slug: "worker-" + uuid.NewString(), Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker, RAMMB: 128, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	newBinding := func(scope string) state.QueueBinding {
		t.Helper()
		binding, e := full.CreateQueueBinding(ctx, state.QueueBinding{AccountID: account.ID, AppID: app.ID, DeploymentScope: scope, Name: "images", QueueName: "images", Mode: "pull", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 1, RetryPolicyJSON: json.RawMessage(`{"max_attempts":3}`)})
		if e != nil {
			t.Fatal(e)
		}
		return binding
	}
	// Destinations resolve in the bucket's environment (ADR-20261104120000
	// amendment 2026-11-16): the staging branch's copy bucket reaches the
	// staging queue, a production bucket only production's.
	binding := newBinding("staging")
	bucket := readyCompanyBucket(t, st, account.ID, app.ID)
	branchBuckets := st.(state.ProjectEnvironmentBranchBucketStore)
	row, err := branchBuckets.RecordProjectEnvironmentBranchBucket(ctx, state.ProjectEnvironmentBranchBucket{AccountID: account.ID,
		ProjectID: project.ID, EnvironmentID: staging.ID, EnvironmentSlug: staging.Slug, AppID: app.ID, SourceBucketID: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	if err = branchBuckets.SetProjectEnvironmentBranchBucketCopy(ctx, row.ID, bucket.ID); err != nil {
		t.Fatal(err)
	}
	notifications := st.(state.ObjectNotificationStore)
	rule := notificationRuleForTest(bucket, app, "queue")
	// The production bucket belongs to another app of the project: the bucket
	// fixture names every bucket "assets".
	producer, err := full.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: account.ID, ProjectID: project.ID, WorkloadName: "producer", Slug: "producer-" + uuid.NewString(), Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker, RAMMB: 128, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	productionBucket := readyCompanyBucket(t, st, account.ID, producer.ID)
	if _, err = notifications.SetObjectBucketNotifications(ctx, account.ID, producer.ID, productionBucket.ID, []api.ObjectNotificationRule{rule}); !errors.Is(err, state.ErrObjectNotificationInvalid) {
		t.Fatal("a production bucket reached a staging-only queue", err)
	}
	if _, err = notifications.SetObjectBucketNotifications(ctx, account.ID, app.ID, bucket.ID, []api.ObjectNotificationRule{rule}); err != nil {
		t.Fatal(err)
	}
	captureFrom := func(bucket state.ObjectBucket) state.ObjectNotificationSnapshot {
		t.Helper()
		completion, e := st.BeginTrackedGatewayUpload(ctx, state.ObjectUploadCompletion{ID: uuid.NewString(), AccountID: account.ID, AppID: bucket.AppID, BucketID: bucket.ID, SubjectID: "test", Key: uuid.NewString(), Status: "pending", Bytes: 1}, accountingPolicy())
		if e != nil {
			t.Fatal(e)
		}
		if completion, e = st.DispatchTrackedObjectUpload(ctx, account.ID, bucket.ID, completion.ID); e != nil {
			t.Fatal(e)
		}
		completion.Status, completion.ETag = "completed", `"done"`
		if _, e = st.FinishTrackedObjectUpload(ctx, completion); e != nil {
			t.Fatal("native mutation settlement", e)
		}
		_, _, work := takeObjectEvent(t, st, account.ID, "write:"+completion.ID, api.ObjectEventCreated) //nolint:contextcheck // Claims with the same test context.
		for _, recipient := range work.RecipientSnapshot {
			if recipient.ObjectNotification != nil {
				return *recipient.ObjectNotification
			}
		}
		t.Fatal("mutation did not capture notification")
		return state.ObjectNotificationSnapshot{}
	}
	snapshot := captureFrom(bucket)
	if snapshot.QueueBindingID != binding.ID || snapshot.DeploymentScope != "staging" || snapshot.EnvironmentID != staging.ID {
		t.Fatal("mutation did not capture staging binding", snapshot)
	}
	// A same-name production queue no longer makes the ARN ambiguous: each
	// bucket resolves its own environment's binding.
	production := newBinding("")
	if again := captureFrom(bucket); again.QueueBindingID != binding.ID {
		t.Fatal("a production queue redirected the staging bucket", again)
	}
	if _, err = notifications.SetObjectBucketNotifications(ctx, account.ID, producer.ID, productionBucket.ID, []api.ObjectNotificationRule{rule}); err != nil {
		t.Fatal(err)
	}
	if own := captureFrom(productionBucket); own.QueueBindingID != production.ID || own.DeploymentScope != "" || own.EnvironmentID != "" {
		t.Fatal("the production bucket did not capture production's binding", own)
	}
	// Existing receipts retain identity and retry policy after a rename/change.
	renamed, retry := "renamed", []byte(`{"max_attempts":7}`)
	if _, err = full.UpdateQueueBinding(ctx, account.ID, app.ID, binding.ID, state.UpdateQueueBindingParams{QueueName: &renamed, RetryPolicyJSON: &retry}); err != nil {
		t.Fatal(err)
	}
	in := notificationInvocationForTest(app, "queue")
	enabled := false
	if _, err = full.UpdateQueueBinding(ctx, account.ID, app.ID, binding.ID, state.UpdateQueueBindingParams{Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	if err = notifications.EnqueueObjectNotification(ctx, in, snapshot); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("disabled captured queue accepted delivery", err)
	}
	enabled = true
	if _, err = full.UpdateQueueBinding(ctx, account.ID, app.ID, binding.ID, state.UpdateQueueBindingParams{Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	if err = notifications.EnqueueObjectNotification(ctx, in, snapshot); err != nil {
		t.Fatal("captured receipt redirected after queue rename", err)
	}
	stored, err := full.InvocationByID(ctx, in.ID)
	if err != nil || stored.QueueBindingID != binding.ID || stored.DeploymentScope != "staging" || stored.EnvironmentID != "" || string(stored.RetryPolicyJSON) != string(snapshot.RetryPolicy) {
		t.Fatal("admission lost captured queue work", stored, err)
	}
	depth, err := full.QueueDepthForBindingInScope(ctx, app.ID, binding.ID, "staging", 10)
	if err != nil || depth != 1 {
		t.Fatal("notification missing from staging backlog", depth, err)
	}
	depth, err = full.QueueDepthForBindingInScope(ctx, app.ID, production.ID, "production", 10)
	if err != nil || depth != 0 {
		t.Fatal("notification leaked into production", depth, err)
	}
	// After the rename the staging bucket resolves nothing, and the production
	// bucket still resolves production's queue.
	if renamedStage := captureFrom(bucket); renamedStage.QueueBindingID != "" {
		t.Fatal("the staging bucket resolved another environment's queue", renamedStage)
	}
	productionSnapshot := captureFrom(productionBucket)
	if productionSnapshot.QueueBindingID != production.ID {
		t.Fatal("unique production destination not captured", productionSnapshot)
	}
	if err = notifications.EnqueueObjectNotification(ctx, notificationInvocationForTest(app, "queue"), productionSnapshot); err != nil {
		t.Fatal("production binding admission", err)
	}
	depth, err = full.QueueDepthForBindingInScope(ctx, app.ID, production.ID, "production", 10)
	if err != nil || depth != 1 {
		t.Fatal("notification missing from production backlog", depth, err)
	}
	if err = full.DeleteQueueBinding(ctx, account.ID, app.ID, binding.ID); err != nil {
		t.Fatal(err)
	}
	if err = notifications.EnqueueObjectNotification(ctx, in, snapshot); err != nil {
		t.Fatal("committed retry required a live queue", err)
	}
	in.ID = uuid.NewString()
	if err = notifications.EnqueueObjectNotification(ctx, in, snapshot); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("retired queue redirected receipt", err)
	}
}
