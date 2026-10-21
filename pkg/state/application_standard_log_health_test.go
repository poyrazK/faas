package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type standardLogHealthTestStore = state.ContractTestStore

func TestMemApplicationStandardLogHealth(t *testing.T) {
	standardLogHealthLifecycle(t, state.NewMemStore())
}

type standardLogHealthExceptionTestStore = state.ContractTestStore

func TestMemApplicationStandardLogHealthExceptionDeadline(t *testing.T) {
	standardLogHealthExceptionDeadline(t, state.NewMemStore())
}

func standardLogHealthExceptionDeadline(t *testing.T, s standardLogHealthExceptionTestStore) {
	t.Helper()
	f := state.NewStandardLocalIntentFixture(t.Context(), t, s)
	c := state.StandardLogInventorySession(t, s, state.StandardLogInventoryNode(t, s).ID)
	r := state.StandardExceptionRequest(f)
	r.ExpiresAt = time.Now().UTC().Add(3 * time.Second)
	x, err := s.ApproveApplicationStandardException(t.Context(), (*f.Owner()).PersonalOrg.ID, (*f.Owner()).Account.ID, (*f.App()).ID, r)
	if err != nil {
		t.Fatal(err)
	}
	(*f.Enrollment()) = state.StandardLocalIntentMaterialize(t.Context(), t, s, f)
	(*f.Company()) = (*f.Rotated()) // This exception replaces the mandatory destination.
	d := state.StandardLogDeliveryDrain(t, s, f)
	e := state.ApplicationStandardLogHealthEvent{EventRevision: 1, Status: "degraded", Reason: "delivery_failed"}
	standardLogHealthWrite(t, s, c, d, e)
	state.WaitStandardExceptionExpiry(t, x)
	if _, err := s.RecordApplicationStandardLogHealth(t.Context(), c, d, e); !errors.Is(err, state.ErrApplicationStandardLogDeliveryStale) {
		t.Fatalf("expired health accepted: %v", err)
	}
	standardLogHealthRead(t, s, f, 0, "")
}

func standardLogHealthLifecycle(t *testing.T, s standardLogHealthTestStore) {
	t.Helper()
	ctx := t.Context()
	f := state.NewStandardLocalIntentFixture(ctx, t, s)
	c := state.StandardLogInventorySession(t, s, state.StandardLogInventoryNode(t, s).ID)
	d := state.StandardLogDeliveryDrain(t, s, f)
	i := state.StandardLogDeliveryInstance(t, s, f)
	e := state.ApplicationStandardLogHealthEvent{EventRevision: 1, Status: "unknown", Reason: "idle"}
	standardLogHealthRefusals(t, s, c, d, e)
	first := standardLogHealthWrite(t, s, c, d, e)
	standardLogHealthRead(t, s, f, 1, "unknown")
	e = state.ApplicationStandardLogHealthEvent{EventRevision: 2, Status: "healthy", Reason: "delivered", SourceInstanceID: i.ID, Sequence: 7}
	healthy := standardLogHealthWrite(t, s, c, d, e)
	heartbeat := standardLogHealthWrite(t, s, c, d, e)
	if !healthy.EventAt.After(first.EventAt) || heartbeat.EventAt != healthy.EventAt || !heartbeat.ObservedAt.After(healthy.ObservedAt) {
		t.Fatal("heartbeat changed event clock or failed to refresh reporter clock")
	}
	e = state.ApplicationStandardLogHealthEvent{EventRevision: 3, Status: "degraded", Reason: "retrying"}
	standardLogHealthWrite(t, s, c, d, e)
	if _, err := s.RecordApplicationStandardLogHealth(ctx, c, d, healthy.ApplicationStandardLogHealthEvent); !errors.Is(err, state.ErrApplicationStandardLogHealthStale) {
		t.Fatalf("late healthy event replaced degradation: %v", err)
	}
	changed := e
	changed.Reason = "delivery_failed"
	if _, err := s.RecordApplicationStandardLogHealth(ctx, c, d, changed); !errors.Is(err, state.ErrApplicationStandardLogHealthStale) {
		t.Fatal("same event identity allowed a different outcome")
	}
	standardLogHealthRead(t, s, f, 1, "degraded")
	newSession := state.StandardLogInventorySession(t, s, c.NodeID)
	standardLogHealthRead(t, s, f, 0, "")
	if _, err := s.RecordApplicationStandardLogHealth(ctx, c, d, e); !errors.Is(err, state.ErrApplicationStandardLogConsumerFenced) {
		t.Fatalf("old process wrote health: %v", err)
	}
	c = newSession
	e = state.ApplicationStandardLogHealthEvent{EventRevision: 1, Status: "unknown", Reason: "idle"}
	standardLogHealthWrite(t, s, c, d, e)
	if err := s.SetComputeNodeActive(ctx, c.NodeID, false); err != nil {
		t.Fatal(err)
	}
	standardLogHealthRead(t, s, f, 0, "")
	if _, err := s.RecordApplicationStandardLogHealth(ctx, c, d, e); !errors.Is(err, state.ErrApplicationStandardLogConsumerFenced) {
		t.Fatalf("inactive node refreshed health: %v", err)
	}
	if err := s.SetComputeNodeActive(ctx, c.NodeID, true); err != nil {
		t.Fatal(err)
	}
	standardLogHealthIntentChange(t, s, f, c, d, e)
}

func standardLogHealthIntentChange(t *testing.T, s standardLogHealthTestStore, f state.StandardLocalIntentFixture, c state.ApplicationStandardLogConsumerSession, d state.AppLogDrain, e state.ApplicationStandardLogHealthEvent) {
	t.Helper()
	ctx := t.Context()
	if _, err := s.SetApplicationStandardLocalIntent(ctx, (*f.Owner()).PersonalOrg.ID, (*f.Owner()).Account.ID, (*f.App()).ID, state.ApplicationStandardLocalIntentRequest{ExpectedRevision: (*f.Enrollment()).DesiredRevision, Settings: json.RawMessage(`{}`), AdditionalLogDestinations: []string{(*f.Extra()).ID}}); err != nil {
		t.Fatal(err)
	}
	standardLogHealthRead(t, s, f, 0, "")
	if _, err := s.RecordApplicationStandardLogHealth(ctx, c, d, e); !errors.Is(err, state.ErrApplicationStandardLogDeliveryStale) {
		t.Fatalf("pending intent accepted old health: %v", err)
	}
	(*f.Enrollment()) = state.StandardLocalIntentMaterialize(ctx, t, s, f)
	if _, err := s.RecordApplicationStandardLogHealth(ctx, c, d, e); !errors.Is(err, state.ErrApplicationStandardLogDeliveryStale) {
		t.Fatal("new intent accepted old projection")
	}
	d = state.StandardLogDeliveryDrain(t, s, f)
	standardLogHealthWrite(t, s, c, d, e)
	standardLogHealthRead(t, s, f, 1, "unknown")
	state.StandardLocalIntentRotateCompany(ctx, t, s, &f)
	standardLogHealthRead(t, s, f, 0, "")
}

func standardLogHealthWrite(t *testing.T, s standardLogHealthTestStore, c state.ApplicationStandardLogConsumerSession, d state.AppLogDrain, e state.ApplicationStandardLogHealthEvent) state.ApplicationStandardLogHealthObservation {
	t.Helper()
	if e.SourceInstanceID != "" {
		e.SourceInstanceID = state.CanonicalStandardUUID(e.SourceInstanceID)
	}
	o, err := s.RecordApplicationStandardLogHealth(t.Context(), c, d, e)
	if err != nil || o.EventAt.IsZero() || o.ObservedAt.Before(o.EventAt) || o.ObservedAt.After(time.Now()) || o.ApplicationStandardLogDrainBinding != *d.StandardBinding || o.ApplicationStandardLogConsumerSession != c || o.ApplicationStandardLogHealthEvent != e {
		t.Fatalf("health write: %+v %v", o, err)
	}
	return o
}

func standardLogHealthRead(t *testing.T, s standardLogHealthTestStore, f state.StandardLocalIntentFixture, count int, status string) {
	t.Helper()
	rows, err := s.ListApplicationStandardLogHealth(t.Context(), (*f.Owner()).PersonalOrg.ID, (*f.App()).ID)
	if err != nil || len(rows) != count || count > 0 && rows[0].Status != status {
		t.Fatalf("health list: %+v %v", rows, err)
	}
	raw, err := json.Marshal(rows)
	if err != nil || strings.Contains(string(raw), "https://") || strings.Contains(string(raw), "sealed-") {
		t.Fatal("health exposed provider configuration")
	}
	enrolled, err := s.GetApplicationStandardEnrollment(t.Context(), (*f.Owner()).PersonalOrg.ID, (*f.App()).ID)
	if err != nil || enrolled.ObservedRevision != 0 {
		t.Fatal("private health advanced application observation")
	}
}

func standardLogHealthRefusals(t *testing.T, s standardLogHealthTestStore, c state.ApplicationStandardLogConsumerSession, d state.AppLogDrain, e state.ApplicationStandardLogHealthEvent) {
	t.Helper()
	for _, bad := range []state.ApplicationStandardLogHealthEvent{
		{EventRevision: 0, Status: "unknown", Reason: "idle"}, {EventRevision: 1, Status: "healthy", Reason: "delivered"},
		{EventRevision: 1, Status: "degraded", Reason: "provider URL and credentials"}, {EventRevision: 1, Status: "healthy", Reason: "delivered", SourceInstanceID: uuid.NewString(), Sequence: -1},
		{EventRevision: api.ApplicationStandardMaxLogHealthEvent, Status: "unknown", Reason: "idle"},
	} {
		if _, err := s.RecordApplicationStandardLogHealth(t.Context(), c, d, bad); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("invalid event accepted: %+v %v", bad, err)
		}
	}
	if _, err := s.ListApplicationStandardLogHealth(t.Context(), uuid.NewString(), d.AppID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("cross-org health read")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.RecordApplicationStandardLogHealth(ctx, c, d, e); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled write accepted: %v", err)
	}
}

func TestMemApplicationStandardLogHealthFreshnessSourceErasureAndCancel(t *testing.T) {
	m := state.NewMemStore()
	f := state.NewStandardLocalIntentFixture(t.Context(), t, m)
	c := state.StandardLogInventorySession(t, m, state.StandardLogInventoryNode(t, m).ID)
	d := state.StandardLogDeliveryDrain(t, m, f)
	i := state.StandardLogDeliveryInstance(t, m, f)
	e := state.ApplicationStandardLogHealthEvent{EventRevision: 2, Status: "healthy", Reason: "delivered", SourceInstanceID: i.ID, Sequence: 3}
	standardLogHealthWrite(t, m, c, d, e)
	(*m.Mu()).Lock()
	delete((*m.Instances()), i.ID)
	(*m.Mu()).Unlock()
	standardLogHealthRead(t, m, f, 0, "")
	e = state.ApplicationStandardLogHealthEvent{EventRevision: 3, Status: "degraded", Reason: "queue_fault"}
	standardLogHealthWrite(t, m, c, d, e)
	(*m.Mu()).Lock()
	for k, o := range *m.ApplicationStandardLogHealth() {
		o.ObservedAt = time.Now().Add(-time.Hour)
		(*m.ApplicationStandardLogHealth())[k] = o
	}
	(*m.Mu()).Unlock()
	standardLogHealthRead(t, m, f, 0, "")
	ctx, cancel := context.WithCancel(t.Context())
	entered, finished := make(chan struct{}), make(chan error, 1)
	(*m.Mu()).Lock()
	go func() { close(entered); _, err := m.RecordApplicationStandardLogHealth(ctx, c, d, e); finished <- err }()
	<-entered
	cancel()
	(*m.Mu()).Unlock()
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatal("canceled lock wait published health")
	}
	if err := m.DeleteComputeNode(t.Context(), c.NodeID); err != nil {
		t.Fatal(err)
	}
	(*m.Mu()).Lock()
	defer (*m.Mu()).Unlock()
	if len((*m.ApplicationStandardLogHealth())) != 0 {
		t.Fatal("node erasure retained health")
	}
}
