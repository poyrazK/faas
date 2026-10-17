package internalmail

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
)

func relayFixture(t *testing.T) *pgFixture {
	t.Helper()
	f := timingFixture(t)
	f.service.BackpressureEnabled = func() bool { return true }
	f.service.ReportsEnabled = func() bool { return true }
	f.service.ReportToken = reportToken
	return f
}

func reportRelay(t *testing.T, f *pgFixture, o RelayObservation) RelayStatus {
	t.Helper()
	status, err := f.service.ReportRelayHealth(t.Context(), o)
	if err != nil {
		t.Fatal(err)
	}
	return status
}

func recoverRelay(t *testing.T, f *pgFixture) RelayStatus {
	t.Helper()
	status := reportRelay(t, f, healthyObservation(time.Now()))
	if status.Reason != "recovering" && status.Reason != "healthy" {
		t.Fatal(status)
	}
	// The pure state tests walk the real two-minute policy in simulated time.
	// PostgreSQL fixtures backdate only the accumulated healthy window.
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_relay_health SET healthy_since=clock_timestamp()-interval '3 minutes' WHERE namespace='platform'"); err != nil {
		t.Fatal(err)
	}
	status = reportRelay(t, f, healthyObservation(time.Now()))
	if status.Held || status.State != "healthy" {
		t.Fatal("relay did not recover", status)
	}
	return status
}

func TestPGRelayBackpressureBootHoldContinuesExpiryWithoutAttempts(t *testing.T) {
	f := relayFixture(t)
	legacy := singleMail(t, f, "unobserved-relay")
	expires := time.Now().Add(650 * time.Millisecond)
	timed := submit(t, f, "expire-under-relay-hold", timedRequest(nil, &expires))
	f.service.Codec = nil
	f.delivery.Store(true)
	for range 3 {
		if err := f.service.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	status, err := f.service.RelayStatus(t.Context())
	if err != nil || !status.Held || status.Reason != "unobserved" || status.Revision != 1 {
		t.Fatal(status, err)
	}
	page, err := f.store.relayEvents(t.Context(), OperatorList{Limit: 25})
	if err != nil || len(page.Items) != 1 {
		t.Fatal("repeated hold duplicated events", err)
	}
	f.delivery.Store(false)
	f.service.SchedulingEnabled = func() bool { return false }
	waitMailTime(t, expires)
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	after, _ := f.store.Message(t.Context(), timed.ID)
	old, _ := f.store.Message(t.Context(), legacy.ID)
	if after.Recipients[0].Status != "expired" || after.Recipients[0].Attempts != 0 || old.Recipients[0].Status != "queued" || old.Recipients[0].Attempts != 0 || len(f.transport.envelopes) != 0 {
		t.Fatal("relay hold lost expiry, attempts or payload fencing")
	}
}

func TestPGRelayBackpressureRecoveryRestartAndManualHolds(t *testing.T) {
	f := relayFixture(t)
	produce(t, f, "auth", "recover-producer")
	status := reportRelay(t, f, healthyObservation(time.Now()))
	if !status.Held || status.Reason != "recovering" {
		t.Fatal("first sample resumed delivery")
	}
	f.service.Store, _ = NewPGStore(f.pool, "platform")
	codec := f.service.Codec
	f.service.Codec = nil
	f.delivery.Store(true)
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	scope := DeliveryScope{Kind: "namespace"}
	controlChange(t, f, scope, "paused", 0)
	recoverRelay(t, f)
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal("recovery bypassed manual hold", err)
	}
	manual, err := f.service.DeliveryStatus(t.Context(), scope)
	if err != nil || manual.Mode != "paused" || manual.Revision != 1 || len(f.transport.envelopes) != 0 {
		t.Fatal("automatic recovery changed operator intent", err)
	}
	other, _ := NewPGStore(f.pool, "other")
	isolated, err := other.relayStatus(t.Context())
	if err != nil || !isolated.Held || isolated.Observation != nil {
		t.Fatal("relay health crossed namespace", err)
	}
	controlChange(t, f, scope, "running", 1)
	f.service.Codec = codec
	if err := f.service.DeliverOne(t.Context()); err != nil || len(f.transport.envelopes) != 1 {
		t.Fatal("healthy resumed mail did not submit", err)
	}
	usage, err := f.service.ProducerUsage(t.Context(), "auth")
	if err != nil || usage.Daily != 1 || usage.Pending != 0 {
		t.Fatal("relay hold changed admission budgets", err)
	}
	bad := healthyObservation(time.Now())
	bad.QueueMessages = api.InternalMailRelayQueueHighWater
	if status := reportRelay(t, f, bad); status.Reason != "queue_depth" || !status.Held {
		t.Fatal(status)
	}
	page, err := f.store.relayEvents(t.Context(), OperatorList{Limit: 1})
	if err != nil || len(page.Items) != 1 || page.Next == "" {
		t.Fatal("bounded relay audit unavailable", err)
	}
	next, err := f.store.relayEvents(t.Context(), OperatorList{Limit: 1, After: page.Next})
	if err != nil || len(next.Items) != 1 || next.Items[0].ID == page.Items[0].ID {
		t.Fatal("relay audit cursor repeated", err)
	}
}

func TestPGRelayBackpressureReplayCannotRefreshHealthOrRecovery(t *testing.T) {
	f := relayFixture(t)
	status := recoverRelay(t, f)
	o := *status.Observation
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			replay, err := f.service.ReportRelayHealth(t.Context(), o)
			if err != nil || replay.Revision != status.Revision || !replay.ReceivedAt.Equal(*status.ReceivedAt) {
				t.Error("replay mutated health", err)
			}
		})
	}
	wg.Wait()
	changed := o
	changed.SMTPUp = false
	if _, err := f.service.ReportRelayHealth(t.Context(), changed); !errors.Is(err, ErrConflict) {
		t.Fatal("changed replay accepted", err)
	}
	older := healthyObservation(o.ObservedAt.Add(-time.Second))
	if _, err := f.service.ReportRelayHealth(t.Context(), older); !errors.Is(err, ErrConflict) {
		t.Fatal("reordered sample accepted", err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_relay_health SET received_at=clock_timestamp()-interval '2 minutes' WHERE namespace='platform'"); err != nil {
		t.Fatal(err)
	}
	replay := reportRelay(t, f, o)
	if !replay.Held || replay.Reason != "stale" || replay.HealthySince != nil || time.Since(*replay.ReceivedAt) < time.Minute {
		t.Fatal("stale replay refreshed receive time", replay)
	}
	restarted, _ := NewPGStore(f.pool, "platform")
	f.service.Store = restarted
	newSample := reportRelay(t, f, healthyObservation(time.Now()))
	if !newSample.Held || newSample.Reason != "recovering" || newSample.HealthySince.Before(time.Now().Add(-time.Second)) {
		t.Fatal("stale gap preserved old recovery window")
	}
	before, _ := f.store.relayStatus(t.Context())
	calls := 0
	_, err := f.store.observeRelay(t.Context(), healthyObservation(time.Now()), func() bool { calls++; return calls == 1 })
	if !errors.Is(err, ErrDisabled) {
		t.Fatal("revoked observation committed", err)
	}
	after, _ := f.store.relayStatus(t.Context())
	if after.Observation.ID != before.Observation.ID || after.Revision != before.Revision {
		t.Fatal("gate rollback left partial health state")
	}
}

func TestPGRelayBackpressureDuringRenderRefundsClaimAndPacing(t *testing.T) {
	f := relayFixture(t)
	recoverRelay(t, f)
	m := singleMail(t, f, "health-during-render")
	f.service.PacingEnabled = func() bool { return true }
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	f.service.Codec = controlBlockingCodec{f.service.Codec, ctx, entered, release}
	f.delivery.Store(true)
	done := make(chan error, 1)
	go func() { done <- f.service.DeliverOne(ctx) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("worker never rendered")
	}
	bad := healthyObservation(time.Now())
	bad.SMTPUp = false
	reportRelay(t, f, bad)
	unblock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("held worker did not settle")
	}
	after, _ := f.store.Message(ctx, m.ID)
	r := after.Recipients[0]
	if r.Status != "queued" || r.Code != "relay_backpressure" || r.Attempts != 0 || len(f.transport.envelopes) != 0 || pacingRows(t, f) != 0 {
		t.Fatal("health hold spent an attempt, a slot or SMTP", r)
	}
	if result := cancellation(t, f, m.ID, true); !result.Complete {
		t.Fatal("backpressure prevented cancellation")
	}
}

func TestPGRelayBackpressureRechecksHealthAfterPacingLockWait(t *testing.T) {
	for _, cause := range []string{"stale", "queue_age"} {
		t.Run(cause, func(t *testing.T) {
			f := relayFixture(t)
			recoverRelay(t, f)
			singleMail(t, f, "health-lock-wait")
			f.delivery.Store(true)
			r, err := f.service.claimMail(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			f.service.PacingEnabled = func() bool { return true }
			deadline := time.Now().Add(650 * time.Millisecond)
			if cause == "stale" {
				if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_relay_health SET observed_at=$1,received_at=$1 WHERE namespace='platform'", deadline.Add(-api.InternalMailRelayMaxSampleAge)); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_relay_health SET queue_messages=1,oldest_seconds=$1,observed_at=$2 WHERE namespace='platform'", int64(api.InternalMailRelayQueueMaxAge/time.Second)-1, deadline.Add(-time.Second)); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.pool.Exec(t.Context(), "INSERT INTO internal_mail_domain_pacing(namespace,recipient_domain,next_allowed_at) VALUES('platform','example.net',clock_timestamp()-interval '1 minute')"); err != nil {
				t.Fatal(err)
			}
			before := pacingSlot(t, f, "platform", "example.net")
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			lock, err := f.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = lock.Rollback(ctx) }()
			if _, err := lock.Exec(ctx, "SELECT next_allowed_at FROM internal_mail_domain_pacing FOR UPDATE"); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- f.service.beginMailSubmission(ctx, r, f.domain.Selector) }()
			select {
			case err := <-done:
				t.Fatal("handoff did not wait", err)
			case <-time.After(30 * time.Millisecond):
			}
			waitMailTime(t, deadline)
			if err := lock.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if !errors.Is(err, ErrRelayHeld) {
					t.Fatal("old transaction time bypassed relay protection", err)
				}
			case <-ctx.Done():
				t.Fatal("health-fenced worker stuck")
			}
			if !pacingSlot(t, f, "platform", "example.net").Equal(before) {
				t.Fatal("health rejection consumed pacing slot")
			}
			status, err := f.store.refreshRelay(ctx)
			if err != nil || !status.Held || status.Reason != cause {
				t.Fatal("hold reason not persisted", status, err)
			}
		})
	}
}

func TestPGRelayBackpressurePreservesInFlightCustodyAndRuntimeRollback(t *testing.T) {
	f := relayFixture(t)
	recoverRelay(t, f)
	first := singleMail(t, f, "in-flight-relay")
	second := singleMail(t, f, "waiting-relay")
	third := singleMail(t, f, "rollback-relay")
	codec := f.service.Codec
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	f.service.Transport = controlTransportFunc(func(ctx context.Context, _ Envelope) (string, string) {
		close(entered)
		select {
		case <-release:
			return "uncertain", "fixture_outcome"
		case <-ctx.Done():
			return "uncertain", "interrupted"
		}
	})
	f.delivery.Store(true)
	done := make(chan error, 1)
	go func() { done <- f.service.DeliverOne(ctx) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("worker never submitted")
	}
	bad := healthyObservation(time.Now())
	bad.InventoryOK = false
	reportRelay(t, f, bad)
	if result := cancellation(t, f, first.ID, true); result.Complete || len(result.Protected) != 1 {
		t.Fatal("backpressure converted in-flight custody")
	}
	unblock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("in-flight worker stuck")
	}
	f.service.Transport = f.transport
	f.service.Codec = nil
	if err := f.service.DeliverOne(ctx); err != nil {
		t.Fatal(err)
	}
	after, _ := f.store.Message(ctx, first.ID)
	waiting, _ := f.store.Message(ctx, second.ID)
	if after.Recipients[0].Status != "uncertain" || waiting.Recipients[0].Attempts != 0 {
		t.Fatal("health pause changed unknown custody or queued attempts")
	}
	if err := f.store.Report(ctx, Report{EventID: uuid.NewString(), RecipientID: first.Recipients[0].ID, Kind: "accepted"}); err != nil {
		t.Fatal("late report blocked by health hold", err)
	}
	if result := cancellation(t, f, second.ID, true); !result.Complete {
		t.Fatal("queued cancellation blocked")
	}
	// Disabling automatic protection must not remove a manual namespace hold.
	controlChange(t, f, DeliveryScope{Kind: "namespace"}, "paused", 0)
	f.service.BackpressureEnabled = func() bool { return false }
	if err := f.service.DeliverOne(ctx); err != nil {
		t.Fatal("runtime rollback bypassed operator hold", err)
	}
	controlChange(t, f, DeliveryScope{Kind: "namespace"}, "running", 1)
	f.service.Codec = codec
	if err := f.service.DeliverOne(ctx); err != nil || len(f.transport.envelopes) != 1 || f.transport.envelopes[0].RecipientID != third.Recipients[0].ID {
		t.Fatal("runtime rollback did not bypass automatic protection", err)
	}
	retained, err := f.store.relayStatus(ctx)
	if err != nil || !retained.Held || retained.Reason != "inventory_unavailable" {
		t.Fatal("runtime rollback discarded durable health", err)
	}
}

func TestPGRelayBackpressureMigrationReplayAndRollbackGuard(t *testing.T) {
	f := relayFixture(t)
	legacy := singleMail(t, f, "legacy-relay-upgrade")
	raw, _ := migrations.FS.ReadFile("20261016112746813_internal_mail_relay_backpressure.sql")
	parts := strings.Split(string(raw), "-- +goose StatementBegin\n")
	up := strings.Split(parts[1], "-- +goose StatementEnd")[0]
	down := strings.Split(parts[2], "-- +goose StatementEnd")[0]
	for range 2 {
		if _, err := f.pool.Exec(t.Context(), down); err != nil {
			t.Fatal(err)
		}
	}
	if f.store.Ready(t.Context()) == nil {
		t.Fatal("old schema passed relay readiness")
	}
	for range 2 {
		if _, err := f.pool.Exec(t.Context(), up); err != nil {
			t.Fatal(err)
		}
	}
	if replay := singleMail(t, f, "legacy-relay-upgrade"); replay.ID != legacy.ID {
		t.Fatal("relay upgrade changed legacy replay")
	}
	reportRelay(t, f, healthyObservation(time.Now()))
	if _, err := f.pool.Exec(t.Context(), down); err == nil {
		t.Fatal("rollback discarded health and transition evidence")
	}
}
