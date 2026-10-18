package state

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type standardLogHealthTestStore interface {
	standardLogDeliveryTestStore
	standardLogInventoryTestStore
	ApplicationStandardLogHealthStore
}

func TestMemApplicationStandardLogHealth(t *testing.T) {
	standardLogHealthLifecycle(t, NewMemStore())
}

type standardLogHealthExceptionTestStore interface {
	standardLogHealthTestStore
	ApplicationStandardExceptionStore
}

func TestMemApplicationStandardLogHealthExceptionDeadline(t *testing.T) {
	standardLogHealthExceptionDeadline(t, NewMemStore())
}

func standardLogHealthExceptionDeadline(t *testing.T, s standardLogHealthExceptionTestStore) {
	t.Helper()
	f := newStandardLocalIntentFixture(t.Context(), t, s)
	c := standardLogInventorySession(t, s, standardLogInventoryNode(t, s).ID)
	r := standardExceptionRequest(f)
	r.ExpiresAt = time.Now().UTC().Add(3 * time.Second)
	x, err := s.ApproveApplicationStandardException(t.Context(), f.owner.PersonalOrg.ID, f.owner.Account.ID, f.app.ID, r)
	if err != nil {
		t.Fatal(err)
	}
	f.enrollment = standardLocalIntentMaterialize(t.Context(), t, s, f)
	f.company = f.rotated // This exception replaces the mandatory destination.
	d := standardLogDeliveryDrain(t, s, f)
	e := ApplicationStandardLogHealthEvent{EventRevision: 1, Status: "degraded", Reason: "delivery_failed"}
	standardLogHealthWrite(t, s, c, d, e)
	waitStandardExceptionExpiry(t, x)
	if _, err := s.RecordApplicationStandardLogHealth(t.Context(), c, d, e); !errors.Is(err, ErrApplicationStandardLogDeliveryStale) {
		t.Fatalf("expired health accepted: %v", err)
	}
	standardLogHealthRead(t, s, f, 0, "")
}

func standardLogHealthLifecycle(t *testing.T, s standardLogHealthTestStore) {
	t.Helper()
	ctx := t.Context()
	f := newStandardLocalIntentFixture(ctx, t, s)
	c := standardLogInventorySession(t, s, standardLogInventoryNode(t, s).ID)
	d := standardLogDeliveryDrain(t, s, f)
	i := standardLogDeliveryInstance(t, s, f)
	e := ApplicationStandardLogHealthEvent{EventRevision: 1, Status: "unknown", Reason: "idle"}
	standardLogHealthRefusals(t, s, c, d, e)
	first := standardLogHealthWrite(t, s, c, d, e)
	standardLogHealthRead(t, s, f, 1, "unknown")
	e = ApplicationStandardLogHealthEvent{EventRevision: 2, Status: "healthy", Reason: "delivered", SourceInstanceID: i.ID, Sequence: 7}
	healthy := standardLogHealthWrite(t, s, c, d, e)
	heartbeat := standardLogHealthWrite(t, s, c, d, e)
	if !healthy.EventAt.After(first.EventAt) || heartbeat.EventAt != healthy.EventAt || !heartbeat.ObservedAt.After(healthy.ObservedAt) {
		t.Fatal("heartbeat changed event clock or failed to refresh reporter clock")
	}
	e = ApplicationStandardLogHealthEvent{EventRevision: 3, Status: "degraded", Reason: "retrying"}
	standardLogHealthWrite(t, s, c, d, e)
	if _, err := s.RecordApplicationStandardLogHealth(ctx, c, d, healthy.ApplicationStandardLogHealthEvent); !errors.Is(err, ErrApplicationStandardLogHealthStale) {
		t.Fatalf("late healthy event replaced degradation: %v", err)
	}
	changed := e
	changed.Reason = "delivery_failed"
	if _, err := s.RecordApplicationStandardLogHealth(ctx, c, d, changed); !errors.Is(err, ErrApplicationStandardLogHealthStale) {
		t.Fatal("same event identity allowed a different outcome")
	}
	standardLogHealthRead(t, s, f, 1, "degraded")
	newSession := standardLogInventorySession(t, s, c.NodeID)
	standardLogHealthRead(t, s, f, 0, "")
	if _, err := s.RecordApplicationStandardLogHealth(ctx, c, d, e); !errors.Is(err, ErrApplicationStandardLogConsumerFenced) {
		t.Fatalf("old process wrote health: %v", err)
	}
	c = newSession
	e = ApplicationStandardLogHealthEvent{EventRevision: 1, Status: "unknown", Reason: "idle"}
	standardLogHealthWrite(t, s, c, d, e)
	if err := s.SetComputeNodeActive(ctx, c.NodeID, false); err != nil {
		t.Fatal(err)
	}
	standardLogHealthRead(t, s, f, 0, "")
	if _, err := s.RecordApplicationStandardLogHealth(ctx, c, d, e); !errors.Is(err, ErrApplicationStandardLogConsumerFenced) {
		t.Fatalf("inactive node refreshed health: %v", err)
	}
	if err := s.SetComputeNodeActive(ctx, c.NodeID, true); err != nil {
		t.Fatal(err)
	}
	standardLogHealthIntentChange(t, s, f, c, d, e)
}

func standardLogHealthIntentChange(t *testing.T, s standardLogHealthTestStore, f standardLocalIntentFixture, c ApplicationStandardLogConsumerSession, d AppLogDrain, e ApplicationStandardLogHealthEvent) {
	t.Helper()
	ctx := t.Context()
	if _, err := s.SetApplicationStandardLocalIntent(ctx, f.owner.PersonalOrg.ID, f.owner.Account.ID, f.app.ID, ApplicationStandardLocalIntentRequest{ExpectedRevision: f.enrollment.DesiredRevision, Settings: json.RawMessage(`{}`), AdditionalLogDestinations: []string{f.extra.ID}}); err != nil {
		t.Fatal(err)
	}
	standardLogHealthRead(t, s, f, 0, "")
	if _, err := s.RecordApplicationStandardLogHealth(ctx, c, d, e); !errors.Is(err, ErrApplicationStandardLogDeliveryStale) {
		t.Fatalf("pending intent accepted old health: %v", err)
	}
	f.enrollment = standardLocalIntentMaterialize(ctx, t, s, f)
	if _, err := s.RecordApplicationStandardLogHealth(ctx, c, d, e); !errors.Is(err, ErrApplicationStandardLogDeliveryStale) {
		t.Fatal("new intent accepted old projection")
	}
	d = standardLogDeliveryDrain(t, s, f)
	standardLogHealthWrite(t, s, c, d, e)
	standardLogHealthRead(t, s, f, 1, "unknown")
	standardLocalIntentRotateCompany(ctx, t, s, &f)
	standardLogHealthRead(t, s, f, 0, "")
}

func standardLogHealthWrite(t *testing.T, s standardLogHealthTestStore, c ApplicationStandardLogConsumerSession, d AppLogDrain, e ApplicationStandardLogHealthEvent) ApplicationStandardLogHealthObservation {
	t.Helper()
	if e.SourceInstanceID != "" {
		e.SourceInstanceID = canonicalStandardUUID(e.SourceInstanceID)
	}
	o, err := s.RecordApplicationStandardLogHealth(t.Context(), c, d, e)
	if err != nil || o.EventAt.IsZero() || o.ObservedAt.Before(o.EventAt) || o.ObservedAt.After(time.Now()) || o.ApplicationStandardLogDrainBinding != *d.StandardBinding || o.ApplicationStandardLogConsumerSession != c || o.ApplicationStandardLogHealthEvent != e {
		t.Fatalf("health write: %+v %v", o, err)
	}
	return o
}

func standardLogHealthRead(t *testing.T, s standardLogHealthTestStore, f standardLocalIntentFixture, count int, status string) {
	t.Helper()
	rows, err := s.ListApplicationStandardLogHealth(t.Context(), f.owner.PersonalOrg.ID, f.app.ID)
	if err != nil || len(rows) != count || count > 0 && rows[0].Status != status {
		t.Fatalf("health list: %+v %v", rows, err)
	}
	raw, err := json.Marshal(rows)
	if err != nil || strings.Contains(string(raw), "https://") || strings.Contains(string(raw), "sealed-") {
		t.Fatal("health exposed provider configuration")
	}
	enrolled, err := s.GetApplicationStandardEnrollment(t.Context(), f.owner.PersonalOrg.ID, f.app.ID)
	if err != nil || enrolled.ObservedRevision != 0 {
		t.Fatal("private health advanced application observation")
	}
}

func standardLogHealthRefusals(t *testing.T, s standardLogHealthTestStore, c ApplicationStandardLogConsumerSession, d AppLogDrain, e ApplicationStandardLogHealthEvent) {
	t.Helper()
	for _, bad := range []ApplicationStandardLogHealthEvent{
		{EventRevision: 0, Status: "unknown", Reason: "idle"}, {EventRevision: 1, Status: "healthy", Reason: "delivered"},
		{EventRevision: 1, Status: "degraded", Reason: "provider URL and credentials"}, {EventRevision: 1, Status: "healthy", Reason: "delivered", SourceInstanceID: uuid.NewString(), Sequence: -1},
		{EventRevision: api.ApplicationStandardMaxLogHealthEvent, Status: "unknown", Reason: "idle"},
	} {
		if _, err := s.RecordApplicationStandardLogHealth(t.Context(), c, d, bad); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("invalid event accepted: %+v %v", bad, err)
		}
	}
	if _, err := s.ListApplicationStandardLogHealth(t.Context(), uuid.NewString(), d.AppID); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-org health read")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.RecordApplicationStandardLogHealth(ctx, c, d, e); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled write accepted: %v", err)
	}
}

func TestMemApplicationStandardLogHealthFreshnessSourceErasureAndCancel(t *testing.T) {
	m := NewMemStore()
	f := newStandardLocalIntentFixture(t.Context(), t, m)
	c := standardLogInventorySession(t, m, standardLogInventoryNode(t, m).ID)
	d := standardLogDeliveryDrain(t, m, f)
	i := standardLogDeliveryInstance(t, m, f)
	e := ApplicationStandardLogHealthEvent{EventRevision: 2, Status: "healthy", Reason: "delivered", SourceInstanceID: i.ID, Sequence: 3}
	standardLogHealthWrite(t, m, c, d, e)
	m.mu.Lock()
	delete(m.instances, i.ID)
	m.mu.Unlock()
	standardLogHealthRead(t, m, f, 0, "")
	e = ApplicationStandardLogHealthEvent{EventRevision: 3, Status: "degraded", Reason: "queue_fault"}
	standardLogHealthWrite(t, m, c, d, e)
	m.mu.Lock()
	for k, o := range m.applicationStandardLogHealth {
		o.ObservedAt = time.Now().Add(-time.Hour)
		m.applicationStandardLogHealth[k] = o
	}
	m.mu.Unlock()
	standardLogHealthRead(t, m, f, 0, "")
	ctx, cancel := context.WithCancel(t.Context())
	entered, finished := make(chan struct{}), make(chan error, 1)
	m.mu.Lock()
	go func() { close(entered); _, err := m.RecordApplicationStandardLogHealth(ctx, c, d, e); finished <- err }()
	<-entered
	cancel()
	m.mu.Unlock()
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatal("canceled lock wait published health")
	}
	if err := m.DeleteComputeNode(t.Context(), c.NodeID); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.applicationStandardLogHealth) != 0 {
		t.Fatal("node erasure retained health")
	}
}
