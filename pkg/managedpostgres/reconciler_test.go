package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestReconcilerRecoversProvisionAndDeletionWhileDisabled(t *testing.T) {
	provider := &fakeProvider{
		capabilities:    testCapabilities(),
		provisionStatus: ProviderStatusPending,
		inspectStatus:   ProviderStatusReady,
	}
	store := NewMemoryStore()
	now := time.Date(2026, 9, 5, 22, 0, 0, 0, time.UTC)
	service, err := NewService(testRegistry(t, provider, nil), store, ServiceOptions{
		LeaseDuration:   2 * time.Minute,
		ProviderTimeout: time.Second,
		PollInterval:    time.Second,
		Now:             func() time.Time { return now },
		NewID:           uuid.NewString,
		NewLeaseToken:   uuid.NewString,
	})
	if err != nil {
		t.Fatal(err)
	}
	enabled := false
	var observations []ReconcileObservation
	reconciler, err := NewReconciler(service, ReconcilerOptions{
		Interval:            time.Second,
		Now:                 func() time.Time { return now },
		IncludeProvisioning: func() bool { return enabled },
		Observe:             func(observation ReconcileObservation) { observations = append(observations, observation) },
	})
	if err != nil {
		t.Fatal(err)
	}

	created, err := service.Create(context.Background(), CreateRequest{AccountID: "account-a", Name: "events", Spec: testSpec()})
	if err != nil || created.ProviderResourceID == "" || created.State != StateProvisioning {
		t.Fatalf("create pending database: %+v %v", created, err)
	}
	now = now.Add(time.Second)
	summary, err := reconciler.Sweep(context.Background())
	if err != nil || summary.Discovered != 0 || provider.inspectCalls != 0 {
		t.Fatalf("disabled sweep: %+v inspect=%d err=%v", summary, provider.inspectCalls, err)
	}

	enabled = true
	summary, err = reconciler.Sweep(context.Background())
	if err != nil || summary.Completed != 1 || provider.provisionCalls != 1 || provider.inspectCalls != 1 {
		t.Fatalf("provision recovery: %+v provision=%d inspect=%d err=%v", summary, provider.provisionCalls, provider.inspectCalls, err)
	}
	ready, err := service.Get(context.Background(), "account-a", created.ID)
	if err != nil || ready.State != StateReady {
		t.Fatalf("ready database: %+v %v", ready, err)
	}

	provider.deleteDone = false
	deleting, err := service.Delete(context.Background(), "account-a", created.ID)
	if err != nil || deleting.State != StateDeleting {
		t.Fatalf("async delete: %+v %v", deleting, err)
	}
	enabled = false
	now = now.Add(time.Second)
	provider.deleteDone = true
	summary, err = reconciler.Sweep(context.Background())
	if err != nil || summary.Completed != 1 || provider.deleteCalls != 2 {
		t.Fatalf("disabled cleanup recovery: %+v delete=%d err=%v", summary, provider.deleteCalls, err)
	}
	deleted, err := service.Get(context.Background(), "account-a", created.ID)
	if err != nil || deleted.State != StateDeleted {
		t.Fatalf("deleted database: %+v %v", deleted, err)
	}
	if len(observations) != 2 || observations[0].Operation != StateProvisioning || observations[1].Operation != StateDeleting {
		t.Fatalf("observations: %+v", observations)
	}
}

func TestReconcilerPersistsExponentialProviderCooldown(t *testing.T) {
	provider := &fakeProvider{
		capabilities:    testCapabilities(),
		provisionStatus: ProviderStatusReady,
		provisionErr:    errors.New("provider token must never persist"),
	}
	store := NewMemoryStore()
	now := time.Date(2026, 9, 5, 22, 30, 0, 0, time.UTC)
	service, err := NewService(testRegistry(t, provider, nil), store, ServiceOptions{
		Now:             func() time.Time { return now },
		NewID:           uuid.NewString,
		NewLeaseToken:   uuid.NewString,
		LeaseDuration:   2 * time.Minute,
		ProviderTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Create(context.Background(), CreateRequest{AccountID: "account-a", Name: "private", Spec: testSpec()})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Create error = %v, want ErrUnavailable", err)
	}
	database, err := store.FindByName(context.Background(), "account-a", "private")
	if err != nil || database.AttemptCount != 1 || database.LastErrorCode != "unavailable" || !database.RetryAt.Equal(now.Add(30*time.Second)) {
		t.Fatalf("first cooldown: %+v %v", database, err)
	}

	reconciler, err := NewReconciler(service, ReconcilerOptions{
		Interval:            time.Second,
		Now:                 func() time.Time { return now },
		IncludeProvisioning: func() bool { return true },
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary, err := reconciler.Sweep(context.Background()); err != nil || summary.Discovered != 0 {
		t.Fatalf("cooldown bypassed: %+v %v", summary, err)
	}
	now = now.Add(30 * time.Second)
	summary, err := reconciler.Sweep(context.Background())
	if err != nil || summary.Deferred != 1 || provider.provisionCalls != 2 {
		t.Fatalf("retry sweep: %+v calls=%d err=%v", summary, provider.provisionCalls, err)
	}
	database, err = store.FindByName(context.Background(), "account-a", "private")
	if err != nil || database.AttemptCount != 2 || !database.RetryAt.Equal(now.Add(time.Minute)) || database.LastErrorCode != "unavailable" {
		t.Fatalf("second cooldown: %+v %v", database, err)
	}
}

func TestReconcilerRunStopsOnCancellation(t *testing.T) {
	provider := &fakeProvider{capabilities: testCapabilities()}
	service := testService(t, testRegistry(t, provider, nil), NewMemoryStore())
	reconciler, err := NewReconciler(service, ReconcilerOptions{Interval: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := reconciler.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
}

func TestRetryDelayIsBounded(t *testing.T) {
	if got := retryDelay(ErrUnavailable, 1); got != 30*time.Second {
		t.Fatalf("first retry = %s", got)
	}
	if got := retryDelay(ErrUnavailable, 30); got != 15*time.Minute {
		t.Fatalf("bounded retry = %s", got)
	}
	if got := retryDelay(ErrUnsupported, 1); got != time.Hour {
		t.Fatalf("unsupported retry = %s", got)
	}
}
