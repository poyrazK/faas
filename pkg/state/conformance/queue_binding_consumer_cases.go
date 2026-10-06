package conformance

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func testQueueBindingConsumerPublication(t *testing.T, fx *Fixture) {
	store, ok := fx.Store.(state.QueueBindingConsumerStore)
	if !ok {
		t.Fatal("queue binding consumer store unavailable")
	}
	if err := fx.Store.UpdateAccountPlan(fx.Ctx, fx.Account.ID, api.PlanHobby); err != nil {
		t.Fatal(err)
	}
	app, err := fx.Store.CreateApp(fx.Ctx, state.App{AccountID: fx.Account.ID, Slug: "queue-atomic-" + uuid.NewString()[:8],
		Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker})
	if err != nil {
		t.Fatal(err)
	}
	request := state.QueueBinding{ID: strings.ReplaceAll(uuid.NewString(), "-", ""), AccountID: fx.Account.ID, AppID: app.ID, Name: "jobs", QueueName: "jobs", Mode: "push",
		WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 5000, RetryPolicyJSON: []byte(`{"max_attempts":25,"base_seconds":2}`)}
	invalid := request
	invalid.ID = "invalid"
	if _, err := store.CreateQueueBindingWithConsumer(fx.Ctx, invalid); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("invalid identity admitted: %v", err)
	}
	created, err := store.CreateQueueBindingWithConsumer(fx.Ctx, request)
	if err != nil || len(created.Changes) != 1 || created.Changes[0].Kind != "created" {
		t.Fatalf("atomic creation: changes=%v err=%v", created.Changes, err)
	}
	triggerID := created.Changes[0].TriggerID
	trigger, err := fx.Store.TriggerByID(fx.Ctx, triggerID)
	limits := api.MustLimitsFor(api.PlanHobby)
	if err != nil || trigger.Slug != "jobs" || trigger.BatchSizeMax != int32(limits.TriggerBatchSizeMax) || trigger.MaxAttempts != int32(limits.TriggerMaxAttemptsMax) {
		t.Fatalf("consumer delivery caps: slug=%q batch=%d attempts=%d err=%v", trigger.Slug, trigger.BatchSizeMax, trigger.MaxAttempts, err)
	}
	if !trigger.QueueBindingID.Valid || trigger.QueueBindingID.Bytes != uuid.MustParse(created.Binding.ID) {
		t.Fatal("consumer lacks authoritative binding identity")
	}
	enabledTriggers, err := fx.Store.ListEnabledTriggers(fx.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	foundConsumer := false
	for _, enabled := range enabledTriggers {
		if enabled.ID == trigger.ID {
			foundConsumer = true
			if enabled.QueueBindingID != trigger.QueueBindingID {
				t.Fatal("scheduler projection lost binding identity")
			}
		}
	}
	if !foundConsumer {
		t.Fatal("enabled consumer missing from scheduler projection")
	}
	disabled := false
	if _, err := fx.Store.UpdateTrigger(fx.Ctx, triggerID, &disabled, nil, nil, nil, nil, nil, nil, nil, nil); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("direct consumer update admitted: %v", err)
	}
	if err := fx.Store.DeleteTrigger(fx.Ctx, triggerID, app.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("direct consumer delete admitted: %v", err)
	}
	if _, err := fx.Store.CreateTriggerIfUnderQuota(fx.Ctx, app.ID, "queue", "forged", false,
		[]byte(`{"mode":"queue","queue_binding_id":null}`), "queue", 1, 1000, 3, 1024, "commit", limits); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("public trigger accepted reserved ownership marker: %v", err)
	}
	var config struct {
		BindingID string             `json:"queue_binding_id"`
		Policy    api.RetryPolicyDTO `json:"retry_policy"`
	}
	if err := json.Unmarshal(trigger.Config, &config); err != nil || config.BindingID != created.Binding.ID || config.Policy.BaseSeconds != 2 {
		t.Fatalf("consumer configuration: %+v err=%v", config, err)
	}
	// Renaming a consumer retains the receipt namespace instead of deleting
	// and recreating its trigger (which would cascade delivery history).
	recordID, err := fx.Store.InsertTriggerRecord(fx.Ctx, triggerID, "receipt", []byte(`{}`), []byte(`{}`), []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	name := "payments"
	updated, err := store.UpdateQueueBindingWithConsumer(fx.Ctx, fx.Account.ID, app.ID, created.Binding.ID, state.UpdateQueueBindingParams{QueueName: &name})
	if err != nil || len(updated.Changes) != 1 || updated.Changes[0].TriggerID != triggerID {
		t.Fatalf("consumer identity changed: %+v err=%v", updated.Changes, err)
	}
	if got, err := fx.Store.TriggerRecordIDByItemIdentifier(fx.Ctx, triggerID, "receipt"); err != nil || got != recordID {
		t.Fatalf("receipt lost on rename: id=%q err=%v", got, err)
	}
	// A slug conflict occurs after binding mutation on PostgreSQL. Both rows
	// must roll back, while unrelated triggers remain intact.
	if _, err := fx.Store.CreateTriggerIfUnderQuota(fx.Ctx, app.ID, "nats", "reserved", false, []byte(`{}`), "", 1, 1000, 3, 1024, "commit", limits); err != nil {
		t.Fatal(err)
	}
	reserved := "reserved"
	if _, err := store.UpdateQueueBindingWithConsumer(fx.Ctx, fx.Account.ID, app.ID, created.Binding.ID, state.UpdateQueueBindingParams{QueueName: &reserved}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("conflicting projection accepted: %v", err)
	}
	if row, err := fx.Store.QueueBindingByID(fx.Ctx, fx.Account.ID, app.ID, created.Binding.ID); err != nil || row.QueueName != "payments" {
		t.Fatalf("failed projection changed binding: name=%q err=%v", row.QueueName, err)
	}
	if row, err := fx.Store.TriggerByID(fx.Ctx, triggerID); err != nil || row.Slug != "payments" {
		t.Fatalf("failed projection changed consumer: slug=%q err=%v", row.Slug, err)
	}
	// A disabled push binding still consumes a trigger quota slot. Fill the
	// account's current app limit; a pull binding can exist, but enabling a
	// new consumer cannot leave a partial projection behind.
	for i := 2; i < limits.TriggerLimitPerApp; i++ {
		if _, err := fx.Store.CreateTriggerIfUnderQuota(fx.Ctx, app.ID, "nats", uuid.NewString(), false, []byte(`{}`), "", 1, 1000, 3, 1024, "commit", limits); err != nil {
			t.Fatal(err)
		}
	}
	request.Name, request.QueueName, request.Mode, request.Enabled = "held", "held", "pull", false
	request.ID = ""
	held, err := store.CreateQueueBindingWithConsumer(fx.Ctx, request)
	if err != nil || len(held.Changes) != 0 {
		t.Fatalf("pull binding: %v", err)
	}
	push := "push"
	var quota *state.TriggerQuotaError
	if _, err := store.UpdateQueueBindingWithConsumer(fx.Ctx, fx.Account.ID, app.ID, held.Binding.ID, state.UpdateQueueBindingParams{Mode: &push}); !errors.As(err, &quota) {
		t.Fatalf("quota denied projection: %v", err)
	}
	if row, err := fx.Store.QueueBindingByID(fx.Ctx, fx.Account.ID, app.ID, held.Binding.ID); err != nil || row.Mode != "pull" {
		t.Fatalf("quota failure changed binding: mode=%q err=%v", row.Mode, err)
	}
	request.Mode = "push"
	request.Name, request.QueueName = "new", "new"
	if _, err := store.CreateQueueBindingWithConsumer(fx.Ctx, request); !errors.As(err, &quota) {
		t.Fatalf("quota failure inserted binding: %v", err)
	}
	bindings, err := fx.Store.ListQueueBindingsForApp(fx.Ctx, fx.Account.ID, app.ID)
	if err != nil || len(bindings) != 2 {
		t.Fatalf("partial creation: bindings=%d err=%v", len(bindings), err)
	}
	if _, err := store.DeleteQueueBindingWithConsumer(fx.Ctx, uuid.NewString(), app.ID, created.Binding.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign delete admitted: %v", err)
	}
	deleted, err := store.DeleteQueueBindingWithConsumer(fx.Ctx, fx.Account.ID, app.ID, created.Binding.ID)
	if err != nil || len(deleted.Changes) != 1 || deleted.Changes[0].TriggerID != triggerID {
		t.Fatalf("atomic deletion: %+v err=%v", deleted.Changes, err)
	}
	if _, err := fx.Store.QueueBindingByID(fx.Ctx, fx.Account.ID, app.ID, created.Binding.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("binding remained after deletion: %v", err)
	}
	if consumer, err := fx.Store.TriggerByID(fx.Ctx, triggerID); err != nil || consumer.Enabled {
		t.Fatalf("retired consumer was lost or enabled: enabled=%t err=%v", consumer.Enabled, err)
	}
	if id, err := fx.Store.TriggerRecordIDByItemIdentifier(fx.Ctx, triggerID, "receipt"); err != nil || id != recordID {
		t.Fatalf("retirement lost receipt: id=%q err=%v", id, err)
	}
}

func testQueueConsumerAccountQuota(t *testing.T, fx *Fixture) {
	store := fx.Store.(state.QueueBindingConsumerStore)
	if err := fx.Store.UpdateAccountPlan(fx.Ctx, fx.Account.ID, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	limits := api.MustLimitsFor(api.PlanPro)
	app := func(accountID string) state.App {
		t.Helper()
		row, err := fx.Store.CreateApp(fx.Ctx, state.App{AccountID: accountID, Slug: "quota-" + uuid.NewString()[:8], Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker})
		if err != nil {
			t.Fatal(err)
		}
		return row
	}
	addTrigger := func(row state.App, name string) error {
		_, err := fx.Store.CreateTriggerIfUnderQuota(fx.Ctx, row.ID, "nats", name, false, []byte(`{}`), "", 1, 1000, 3, 1024, "commit", limits)
		return err
	}
	var fillers []state.App
	for i := 0; i < limits.TriggerLimitPerAccount-1; i++ {
		if i%limits.TriggerLimitPerApp == 0 {
			fillers = append(fillers, app(fx.Account.ID))
		}
		if err := addTrigger(fillers[len(fillers)-1], uuid.NewString()); err != nil {
			t.Fatal(err)
		}
	}
	foreign, err := fx.Store.CreateAccount(fx.Ctx, uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if err := addTrigger(app(foreign.ID), "foreign"); err != nil {
		t.Fatalf("other account inherited quota: %v", err)
	}
	consumerApp, triggerApp := app(fx.Account.ID), app(fx.Account.ID)
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		<-start
		_, err := store.CreateQueueBindingWithConsumer(fx.Ctx, state.QueueBinding{AccountID: fx.Account.ID, AppID: consumerApp.ID,
			Name: "jobs", QueueName: "jobs", Mode: "push", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 1})
		results <- err
	}()
	go func() { <-start; results <- addTrigger(triggerApp, "ordinary") }()
	close(start)
	succeeded, denied := 0, 0
	for i := 0; i < 2; i++ {
		err := <-results
		var quota *state.TriggerQuotaError
		if err == nil {
			succeeded++
		} else if errors.As(err, &quota) && quota.Scope == state.TriggerQuotaScopeAccount {
			denied++
		} else {
			t.Fatalf("concurrent account admission: %v", err)
		}
	}
	if succeeded != 1 || denied != 1 {
		t.Fatalf("shared account quota: succeeded=%d denied=%d", succeeded, denied)
	}
	count := 0
	for _, row := range append(fillers, consumerApp, triggerApp) {
		triggers, err := fx.Store.ListTriggersForApp(fx.Ctx, row.ID)
		if err != nil {
			t.Fatal(err)
		}
		count += len(triggers)
	}
	if count != limits.TriggerLimitPerAccount {
		t.Fatalf("account triggers=%d want=%d", count, limits.TriggerLimitPerAccount)
	}
}
