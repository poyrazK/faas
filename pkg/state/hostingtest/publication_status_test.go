package hostingtest

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func publicationStatusLifecycle(t *testing.T, store hostingTestStore) {
	before, err := store.HostingRouteBacklog(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	account, site := seedDurable(t, store)
	initial, err := store.HostingRouteSnapshot(t.Context(), site.ID)
	if err != nil || initial.PendingSince.IsZero() || initial.NextAttemptAt.IsZero() {
		t.Fatalf("initial publication status: %+v %v", initial, err)
	}
	at := time.Now()
	if err = store.RetryHostingRoute(t.Context(), site.ID, 1, at, api.HostingRouteErrorProvider); err != nil {
		t.Fatal(err)
	}
	if _, err = store.SetHostingSiteSuspended(t.Context(), account.ID, site.ID, site.Revision, true); err != nil {
		t.Fatal(err)
	}
	if err = store.RetryHostingRoute(t.Context(), site.ID, 1, at.Add(24*time.Hour), api.HostingRouteErrorOrigin); err != nil {
		t.Fatal(err)
	}
	current, err := store.HostingRouteSnapshot(t.Context(), site.ID)
	if err != nil || current.Generation != 2 || current.RetryCount != 0 || current.LastErrorCode != "" || !current.LastAttemptAt.IsZero() || !current.PendingSince.Equal(initial.PendingSince) {
		t.Fatalf("new intent inherited stale attempt or lost backlog age: %+v %v", current, err)
	}
	if err = store.RetryHostingRoute(t.Context(), site.ID, 2, at, api.HostingRouteErrorStale); err != nil {
		t.Fatal(err)
	}
	if err = store.AcknowledgeHostingRoute(t.Context(), site.ID, 1, at); err != nil {
		t.Fatal(err)
	}
	current, err = store.HostingRouteSnapshot(t.Context(), site.ID)
	if err != nil || current.RetryCount != 1 || current.LastErrorCode != api.HostingRouteErrorStale || current.PendingSince.IsZero() {
		t.Fatalf("old acknowledgement erased new pending work: %+v %v", current, err)
	}
	backlog, err := store.HostingRouteBacklog(t.Context())
	if err != nil || backlog.Pending != before.Pending+1 || backlog.OldestPendingAt.IsZero() {
		t.Fatalf("backlog: %+v %v", backlog, err)
	}
	if err = store.AcknowledgeHostingRoute(t.Context(), site.ID, 2, at); err != nil {
		t.Fatal(err)
	}
	if err = store.RetryHostingRoute(t.Context(), site.ID, 2, at.Add(time.Minute), api.HostingRouteErrorProvider); err != nil {
		t.Fatal(err)
	}
	backlog, err = store.HostingRouteBacklog(t.Context())
	if err != nil || backlog.Pending != before.Pending {
		t.Fatalf("failed repair counted as unpublished intent: %+v %v", backlog, err)
	}
	if err = store.AcknowledgeHostingRoute(t.Context(), site.ID, 2, at.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	current, err = store.HostingRouteSnapshot(t.Context(), site.ID)
	// PostgreSQL stores microsecond precision.
	if err != nil || !current.PendingSince.IsZero() || current.RetryCount != 0 || current.PublishedAt.Sub(at).Abs() >= time.Microsecond || current.LastAttemptAt.Sub(at.Add(2*time.Minute)).Abs() >= time.Microsecond {
		t.Fatalf("repair rewrote publication time or failed to clear retry: %+v %v", current, err)
	}
	if err = store.RetryHostingRoute(t.Context(), site.ID, 2, at, "https://secret-provider-error.example"); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("unbounded provider error accepted: %v", err)
	}
}
