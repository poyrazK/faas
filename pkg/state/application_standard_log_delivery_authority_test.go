package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type standardLogExceptionTestStore interface {
	standardLogDeliveryTestStore
	ApplicationStandardExceptionStore
}

func TestMemApplicationStandardLogDeliveryExceptionDeadline(t *testing.T) {
	standardLogDeliveryExceptionDeadline(t, NewMemStore())
}

func standardLogDeliveryExceptionDeadline(t *testing.T, s standardLogExceptionTestStore) {
	t.Helper()
	ctx := t.Context()
	f := newStandardLocalIntentFixture(ctx, t, s)
	ins := standardLogDeliveryInstance(t, s, f)
	r := standardExceptionRequest(f)
	r.ExpiresAt = time.Now().UTC().Add(3 * time.Second)
	x, err := s.ApproveApplicationStandardException(ctx, f.owner.PersonalOrg.ID, f.owner.Account.ID, f.app.ID, r)
	if err != nil {
		t.Fatal(err)
	}
	f.enrollment = standardLocalIntentMaterialize(ctx, t, s, f)
	f.company = f.rotated
	d := standardLogDeliveryDrain(t, s, f)
	if _, err := s.RecordApplicationStandardLogDelivery(ctx, d, ins.ID, 1); err != nil {
		t.Fatal(err)
	}
	waitStandardExceptionExpiry(t, x)
	if _, err := s.RecordApplicationStandardLogDelivery(ctx, d, ins.ID, 2); !errors.Is(err, ErrApplicationStandardLogDeliveryStale) {
		t.Fatalf("expired receipt accepted: %v", err)
	}
	rows, err := s.ListEnabledAppLogDrains(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if sameStandardUUID(row.AppID, f.app.ID) && row.StandardBinding != nil {
			t.Fatal("expired projection acquired a binding before repair")
		}
	}
	e, err := s.GetApplicationStandardEnrollment(ctx, f.owner.PersonalOrg.ID, f.app.ID)
	if err != nil || e.ObservedRevision != 0 || e.DesiredRevision != f.enrollment.DesiredRevision || e.State != "persisted" {
		t.Fatal("delivery refusal fabricated repair or observation")
	}
}

func TestMemApplicationStandardLogDeliveryParentAuthority(t *testing.T) {
	standardLogDeliveryParentAuthority(t, NewMemStore())
}

func standardLogDeliveryParentAuthority(t *testing.T, s standardLogDeliveryTestStore) {
	t.Helper()
	ctx := t.Context()
	f := newStandardLocalIntentFixture(ctx, t, s)
	ins := standardLogDeliveryInstance(t, s, f)
	d := standardLogDeliveryDrain(t, s, f)
	other := standardLogDeliveryForeignSource(t, s)
	if _, err := s.RecordApplicationStandardLogDelivery(ctx, d, other.ID, 1); !errors.Is(err, ErrApplicationStandardLogDeliveryStale) {
		t.Fatal("cross-application source accepted")
	}
	if err := s.UpdateAccountStatus(ctx, f.owner.Account.ID, AccountSuspended); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordApplicationStandardLogDelivery(ctx, d, ins.ID, 1); !errors.Is(err, ErrApplicationStandardLogDeliveryStale) {
		t.Fatal("suspended owner accepted")
	}
	if err := s.UpdateAccountStatus(ctx, f.owner.Account.ID, AccountActive); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordApplicationStandardLogDelivery(ctx, d, ins.ID, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ScheduleAppDeletion(ctx, f.app.ID, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordApplicationStandardLogDelivery(ctx, d, ins.ID, 2); !errors.Is(err, ErrApplicationStandardLogDeliveryStale) {
		t.Fatal("deleted app accepted")
	}
	if _, err := s.ListApplicationStandardLogDeliveries(ctx, f.owner.PersonalOrg.ID, f.app.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted app receipts exposed")
	}
	if err := s.ClaimAppDeletion(ctx, f.app.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAppPermanently(ctx, f.app.ID); err != nil {
		t.Fatal(err)
	}
}

func standardLogDeliveryForeignSource(t *testing.T, s standardLogDeliveryTestStore) Instance {
	t.Helper()
	ctx := t.Context()
	owner, err := s.CreateAccountWithPersonalOrg(ctx, CreateAccountWithPersonalOrgParams{Email: "foreign-log-source@example.com", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, App{AccountID: owner.Account.ID, OrgID: owner.PersonalOrg.ID, Slug: "foreign-log-source", RAMMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	f := standardLocalIntentFixture{app: app}
	return standardLogDeliveryInstance(t, s, f)
}

func TestMemApplicationStandardLogDeliveryParentErasure(t *testing.T) {
	m := NewMemStore()
	f := newStandardLocalIntentFixture(t.Context(), t, m)
	ins := standardLogDeliveryInstance(t, m, f)
	d := standardLogDeliveryDrain(t, m, f)
	if _, err := m.RecordApplicationStandardLogDelivery(t.Context(), d, ins.ID, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ScheduleAppDeletion(t.Context(), f.app.ID, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := m.ClaimAppDeletion(t.Context(), f.app.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteAppPermanently(t.Context(), f.app.ID); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.applicationStandardLogDeliveries {
		if sameStandardUUID(r.AppID, f.app.ID) {
			t.Fatal("erased app retained private receipt")
		}
	}
}

func TestMemApplicationStandardLogDeliveryMalformedHash(t *testing.T) {
	d := AppLogDrain{ID: uuid.NewString(), AppID: uuid.NewString(), AccountID: uuid.NewString(), Enabled: true}
	d.StandardBinding = &ApplicationStandardLogDrainBinding{OrgID: uuid.NewString(), AppID: d.AppID, DrainID: d.ID, ResourceID: uuid.NewString(), DesiredRevision: 1, DrainConfigHash: ApplicationStandardLogDrainConfigHash(d), EffectiveHash: "bad", ResourceConfigHash: "bad"}
	if _, err := NewMemStore().RecordApplicationStandardLogDelivery(t.Context(), d, uuid.NewString(), 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal("malformed receipt accepted")
	}
}

func TestMemApplicationStandardLogDeliveryCanceledWhileWaiting(t *testing.T) {
	m := NewMemStore()
	f := newStandardLocalIntentFixture(t.Context(), t, m)
	ins := standardLogDeliveryInstance(t, m, f)
	d := standardLogDeliveryDrain(t, m, f)
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	m.mu.Lock()
	result := make(chan error, 1)
	go func() { _, err := m.RecordApplicationStandardLogDelivery(ctx, d, ins.ID, 1); result <- err }()
	<-ctx.Done()
	m.mu.Unlock()
	if err := <-result; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("canceled receipt persisted: %v", err)
	}
	rows, err := m.ListApplicationStandardLogDeliveries(t.Context(), f.owner.PersonalOrg.ID, f.app.ID)
	if err != nil || len(rows) != 0 {
		t.Fatal("canceled receipt left evidence")
	}
}
