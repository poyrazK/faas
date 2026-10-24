package state_test

import (
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 552
func TestObjectNotificationConfigurationAndAdmissionMem(t *testing.T) {
	st := state.NewMemStore()
	objectNotificationConfigurationAndAdmission(t, st, func(t *testing.T, appID, accountID string, n int) {
		for range n {
			if _, err := st.EnqueueInvocation(t.Context(), state.Invocation{AppID: appID, AccountID: accountID, Source: state.InvocationQueue, QueueName: "filler", Payload: json.RawMessage(`{}`), DueAt: time.Now()}); err != nil {
				t.Fatal(err)
			}
		}
	})
}
func TestObjectNotificationConfigurationAndAdmissionPG(t *testing.T) {
	st, _, pool := pgWithPool(t)
	objectNotificationConfigurationAndAdmission(t, st, func(t *testing.T, appID, accountID string, n int) {
		if _, err := pool.Exec(t.Context(), `INSERT INTO invocations (app_id,account_id,source,queue_name,deployment_scope)
SELECT $1,$2,'queue','filler','default' FROM generate_series(1,$3)`, appID, accountID, n); err != nil {
			t.Fatal(err)
		}
	})
}

// prefill queues n pending messages on another of the app's queues; they
// count toward the per-app MaxQueueDepth like any other queued message.
func objectNotificationConfigurationAndAdmission(t *testing.T, st accountingStore, prefill func(t *testing.T, appID, accountID string, n int)) {
	b, _ := seedAccounting(t, st)
	ctx := t.Context()
	full := st.(state.Store)
	notifications := st.(state.ObjectNotificationStore)
	app, err := full.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: b.AccountID, Slug: "notify-" + uuid.NewString(), Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 512, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	qb, err := full.CreateQueueBinding(ctx, state.QueueBinding{AccountID: b.AccountID, AppID: app.ID, Name: "notify", QueueName: "notify", Mode: "pull", WorkloadClass: "worker", Enabled: true, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	arn := "arn:gregale:sqs:" + b.Region + ":" + uuid.MustParse(b.AccountID).String() + ":" + app.ID + "/notify"
	r := api.ObjectNotificationRule{ID: "images", Destination: arn, Events: []string{"s3:ObjectCreated:Put"}, Prefix: "images/"}
	p, err := notifications.SetObjectBucketNotifications(ctx, b.AccountID, b.AppID, b.ID, []api.ObjectNotificationRule{r})
	if err != nil || p.Revision != 1 {
		t.Fatal(p, err)
	}
	p.Rules[0].Events[0] = "evil"
	p, err = notifications.GetObjectBucketNotifications(ctx, b.AccountID, b.AppID, b.ID)
	if err != nil || p.Rules[0].Events[0] != r.Events[0] {
		t.Fatal("caller changed owned policy", p, err)
	}
	p, err = notifications.SetObjectBucketNotifications(ctx, b.AccountID, b.AppID, b.ID, []api.ObjectNotificationRule{r})
	if err != nil || p.Revision != 1 {
		t.Fatal("idempotent revision", p, err)
	}
	bad := r
	bad.Destination = "arn:gregale:sqs:" + b.Region + ":" + uuid.NewString() + ":" + app.ID + "/notify"
	if _, err = notifications.SetObjectBucketNotifications(ctx, b.AccountID, b.AppID, b.ID, []api.ObjectNotificationRule{bad}); !errors.Is(err, state.ErrObjectNotificationInvalid) {
		t.Fatal("foreign destination", err)
	}
	p, err = notifications.GetObjectBucketNotifications(ctx, b.AccountID, b.AppID, b.ID)
	if err != nil || p.Revision != 1 {
		t.Fatal("invalid replacement changed state", p, err)
	}
	if _, err = notifications.GetObjectBucketNotifications(ctx, uuid.NewString(), b.AppID, b.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("foreign bucket read", err)
	}
	snapshot := state.ObjectNotificationSnapshot{Rule: r, BucketName: b.Name, BucketRegion: b.Region, QueueBindingID: qb.ID, RetryPolicy: json.RawMessage(`{}`)}
	acct, err := full.AccountByID(ctx, b.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	// Fill all but the last few slots, then race twice as many admissions as
	// remain: exactly the open slots may win. Racing the whole cap spawned
	// 2×MaxQueueDepth goroutines, which ADR-20261022105723 raised to 10,000+.
	const open = 8
	prefill(t, app.ID, b.AccountID, api.MustLimitsFor(acct.Plan).MaxQueueDepth-open)
	var winners atomic.Int32
	var wg sync.WaitGroup
	ids := make(chan string, open*2)
	for i := 0; i < open*2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			in := state.Invocation{ID: uuid.NewString(), AppID: app.ID, AccountID: b.AccountID, Source: state.InvocationQueue, QueueName: "notify", Payload: json.RawMessage(`{"Records":[]}`), Headers: json.RawMessage(`{}`), DueAt: time.Now()}
			e := notifications.EnqueueObjectNotification(ctx, in, snapshot)
			if e == nil {
				winners.Add(1)
				ids <- in.ID
			} else if !errors.Is(e, state.ErrObjectNotificationCapacity) {
				t.Errorf("admission error: %v", e)
			}
		}()
	}
	wg.Wait()
	close(ids)
	if int(winners.Load()) != open {
		t.Fatal("concurrent admission cap", winners.Load(), open)
	}
	id := <-ids
	replay := state.Invocation{ID: id, AppID: app.ID, AccountID: b.AccountID, Source: state.InvocationQueue, QueueName: "notify", Payload: json.RawMessage(`{"Records":[]}`), Headers: json.RawMessage(`{}`), DueAt: time.Now()}
	if err = notifications.EnqueueObjectNotification(ctx, replay, snapshot); err != nil {
		t.Fatal("replay consumed another slot", err)
	}
	replay.Payload = json.RawMessage(`{"Records":["forged"]}`)
	if err = notifications.EnqueueObjectNotification(ctx, replay, snapshot); !errors.Is(err, state.ErrConflict) {
		t.Fatal("identity collision accepted", err)
	}
	if err = full.CancelInvocation(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err = full.DeleteQueueBinding(ctx, b.AccountID, app.ID, qb.ID); err != nil {
		t.Fatal(err)
	}
	// Retirement retains the original destination's identity and reserves its
	// names while queued receipts still reference it.
	if _, err = full.CreateQueueBinding(ctx, state.QueueBinding{AccountID: b.AccountID, AppID: app.ID, Name: "notify", QueueName: "notify", Mode: "pull", WorkloadClass: "worker", Enabled: true, MaxConcurrency: 1}); !errors.Is(err, state.ErrConflict) {
		t.Fatal("retired destination names reused", err)
	}
	newBinding, err := full.CreateQueueBinding(ctx, state.QueueBinding{AccountID: b.AccountID, AppID: app.ID, Name: "notify-replacement", QueueName: "notify-replacement", Mode: "pull", WorkloadClass: "worker", Enabled: true, MaxConcurrency: 1})
	if err != nil || newBinding.ID == qb.ID {
		t.Fatal(newBinding, err)
	}
	replay.ID = uuid.NewString()
	if err = notifications.EnqueueObjectNotification(ctx, replay, snapshot); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("removed target redirected to new binding", err)
	}
	p, err = notifications.SetObjectBucketNotifications(ctx, b.AccountID, b.AppID, b.ID, nil)
	if err != nil || p.Revision != 2 || len(p.Rules) != 0 {
		t.Fatal("clear policy", p, err)
	}
}
