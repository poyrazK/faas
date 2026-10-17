package internalmail

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
)

func pacingFixture(t *testing.T) *pgFixture {
	t.Helper()
	f := timingFixture(t)
	f.service.PacingEnabled = func() bool { return true }
	f.service.PacingPolicy.Interval = api.InternalMailMaxDomainPacingInterval
	return f
}

func pacingSlot(t *testing.T, f *pgFixture, namespace, domain string) time.Time {
	t.Helper()
	var at time.Time
	if err := f.pool.QueryRow(t.Context(), "SELECT next_allowed_at FROM internal_mail_domain_pacing WHERE namespace=$1 AND recipient_domain=$2", namespace, domain).Scan(&at); err != nil {
		t.Fatal(err)
	}
	return at
}

func pacingRows(t *testing.T, f *pgFixture) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM internal_mail_domain_pacing").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPGDomainPacingConcurrentWorkersRestartAndDisable(t *testing.T) {
	f := pacingFixture(t)
	second, err := f.service.RegisterDomain(t.Context(), "sender.example")
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range second.Records {
		f.resolver[record.Name] = []string{record.Value}
	}
	if _, err := f.service.VerifyDomain(t.Context(), second.ID); err != nil {
		t.Fatal(err)
	}
	var messages []MessageResponse
	for i := range 4 {
		input := producerRequest()
		if i%2 == 1 {
			input.From = "notify@sender.example"
		}
		messages = append(messages, submit(t, f, uuid.NewString(), input))
	}
	f.delivery.Store(true)
	var submitted atomic.Int32
	f.service.Transport = controlTransportFunc(func(context.Context, Envelope) (string, string) {
		submitted.Add(1)
		return "submitted", "relay_accepted"
	})
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	// Claim all contenders before any begins. Different sender-domain locks
	// cannot substitute for the shared recipient-domain reservation.
	var recipients []Recipient
	for range messages {
		r, err := f.service.claimMail(ctx)
		if err != nil {
			t.Fatal(err)
		}
		recipients = append(recipients, r)
	}
	var wg sync.WaitGroup
	for _, r := range recipients {
		worker := *f.service
		worker.Store, err = NewPGStore(f.pool, "platform")
		if err != nil {
			t.Fatal(err)
		}
		wg.Go(func() {
			status, code := worker.prepareAndSubmit(ctx, r)
			if err := worker.settleMail(ctx, r, status, code, time.Now()); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if submitted.Load() != 1 || pacingRows(t, f) != 1 {
		t.Fatal("concurrent workers exceeded one shared domain slot", submitted.Load())
	}
	slot := pacingSlot(t, f, "platform", "example.net")
	waiting := 0
	for _, m := range messages {
		after, err := f.store.operatorMessage(ctx, m.ID)
		if err != nil {
			t.Fatal(err)
		}
		r := after.Recipients[0]
		if r.Status == "queued" {
			waiting++
			if r.Attempts != 0 || r.Code != pacingWaitCode || !r.NextAttemptAt.Equal(slot) {
				t.Fatal("pacing consumed an attempt or lost the durable retry time", r)
			}
			events, err := f.store.Events(ctx, m.ID)
			if err != nil || len(events) != 1 || events[0].Kind != "queued" {
				t.Fatal("pacing added a failed delivery event", err)
			}
		} else if r.Status != "submitted" || r.Attempts != 1 {
			t.Fatal("unexpected pacing settlement", r)
		}
	}
	if waiting != 3 {
		t.Fatal("concurrent paced work was lost")
	}
	stats, err := f.store.PacingStatistics(ctx)
	if err != nil || stats.CoolingDomains != 1 || stats.WaitingRecipients != 3 {
		t.Fatal("paced inventory aggregates incorrect", stats, err)
	}
	codec := f.service.Codec
	f.service.Store, _ = NewPGStore(f.pool, "platform")
	f.service.Codec = nil
	if err := f.service.DeliverOne(ctx); err != nil || submitted.Load() != 1 {
		t.Fatal("restart bypassed cooldown or decrypted blocked work", err)
	}
	f.service.Codec = codec
	f.service.PacingEnabled = func() bool { return false }
	if err := f.service.DeliverOne(ctx); err != nil || submitted.Load() != 2 {
		t.Fatal("disabling pacing did not bypass its recorded queue wait", err)
	}
	if !pacingSlot(t, f, "platform", "example.net").Equal(slot) {
		t.Fatal("disabled pacing mutated saved cooldown")
	}
}

func TestPGDomainPacingSkipsBlockedDomainAndIsolatesNamespace(t *testing.T) {
	f := pacingFixture(t)
	singleMail(t, f, "first-domain")
	blocked := singleMail(t, f, "same-domain")
	input := producerRequest()
	input.To = []string{"other@another.example"}
	other := submit(t, f, "other-domain", input)
	f.delivery.Store(true)
	for range 2 {
		if err := f.service.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	after, _ := f.store.Message(t.Context(), blocked.ID)
	otherAfter, _ := f.store.Message(t.Context(), other.ID)
	if len(f.transport.envelopes) != 2 || after.Recipients[0].Attempts != 0 || after.Recipients[0].Status != "queued" || otherAfter.Recipients[0].Status != "submitted" {
		t.Fatal("one paced domain blocked another domain or spent attempts")
	}
	// A different namespace can reserve the same domain while platform cools.
	store, _ := NewPGStore(f.pool, "separate")
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if err := store.reserveDomainPacing(t.Context(), tx, "one@example.net", f.service.PacingPolicy); err != nil {
		t.Fatal("pacing crossed namespace boundaries", err)
	}
	if err := tx.Commit(t.Context()); err != nil || pacingRows(t, f) != 3 {
		t.Fatal("separate namespace reservation failed", err)
	}
	stats, err := f.store.PacingStatistics(t.Context())
	if err != nil || stats.CoolingDomains != 2 || stats.WaitingRecipients != 1 {
		t.Fatal("pacing statistics crossed namespaces", err, stats)
	}
}

func TestPGDomainPacingHonorsRealCooldownAndFutureSchedule(t *testing.T) {
	f := pacingFixture(t)
	f.service.PacingPolicy.Interval = time.Second
	sendAt := time.Now().Add(800 * time.Millisecond)
	input := timedRequest(&sendAt, timeRef(sendAt.Add(time.Minute)))
	m := submit(t, f, "future-paced", input)
	second := singleMail(t, f, "immediate-paced")
	f.delivery.Store(true)
	if err := f.service.DeliverOne(t.Context()); err != nil || len(f.transport.envelopes) != 1 || f.transport.envelopes[0].RecipientID != second.Recipients[0].ID {
		t.Fatal("future schedule consumed a pacing slot", err)
	}
	at := pacingSlot(t, f, "platform", "example.net")
	if at.Before(sendAt) || at.After(time.Now().Add(1100*time.Millisecond)) {
		t.Fatal("domain slot did not use the configured interval")
	}
	codec := f.service.Codec
	f.service.Codec = nil
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal("future work was claimed before schedule and cooldown", err)
	}
	waitMailTime(t, at)
	f.service.Codec = codec
	if err := f.service.DeliverOne(t.Context()); err != nil || len(f.transport.envelopes) != 2 || f.transport.envelopes[1].RecipientID != m.Recipients[0].ID {
		t.Fatal("paced schedule did not become eligible", err)
	}
	if !pacingSlot(t, f, "platform", "example.net").After(at) {
		t.Fatal("second submission did not advance the cooldown")
	}
}

func TestPGDomainPacingExpiresBlockedMailWithoutSMTP(t *testing.T) {
	f := pacingFixture(t)
	singleMail(t, f, "occupy-slot")
	expires := time.Now().Add(700 * time.Millisecond)
	m := submit(t, f, "blocked-expiry", timedRequest(nil, &expires))
	f.delivery.Store(true)
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	slot := pacingSlot(t, f, "platform", "example.net")
	f.service.Codec = nil
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	waitMailTime(t, expires)
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	after, _ := f.store.Message(t.Context(), m.ID)
	if after.Recipients[0].Status != "expired" || after.Recipients[0].Attempts != 0 || len(f.transport.envelopes) != 1 || !pacingSlot(t, f, "platform", "example.net").Equal(slot) {
		t.Fatal("cooldown bypassed expiry or used another slot")
	}
}

func TestPGDomainPacingPreservesHoldsCancellationAndBudgets(t *testing.T) {
	f := pacingFixture(t)
	produce(t, f, "auth", "first-producer")
	blocked := produce(t, f, "billing", "paced-producer")
	f.delivery.Store(true)
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	scope := DeliveryScope{Kind: "namespace"}
	controlChange(t, f, scope, "paused", 0)
	f.service.PacingEnabled = func() bool { return false }
	f.service.Codec = nil
	if err := f.service.DeliverOne(t.Context()); err != nil || len(f.transport.envelopes) != 1 {
		t.Fatal("pacing rollback bypassed durable hold", err)
	}
	if result := cancellation(t, f, blocked.ID, true); !result.Complete {
		t.Fatal("pacing blocked queued cancellation")
	}
	controlChange(t, f, scope, "running", 1)
	if err := f.service.DeliverOne(t.Context()); err != nil || len(f.transport.envelopes) != 1 {
		t.Fatal("pacing rollback revived cancellation", err)
	}
	usage, err := f.service.ProducerUsage(t.Context(), "billing")
	if err != nil || usage.Daily != 1 || usage.Pending != 0 {
		t.Fatal("pacing changed producer budget accounting", usage, err)
	}
}

func TestPGDomainPacingReservationRollsBackAfterLockWait(t *testing.T) {
	for _, expired := range []string{"message", "lease", "authorization"} {
		t.Run(expired, func(t *testing.T) {
			f := pacingFixture(t)
			input := producerRequest()
			deadline := time.Now().Add(650 * time.Millisecond)
			if expired == "message" {
				input.ExpiresAt = &deadline
			}
			m := submit(t, f, "lock-wait", input)
			if expired == "authorization" {
				f.service.ReverificationEnabled = func() bool { return true }
				if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_domains SET last_verified_at=$1,verified_sending_ip=$2 WHERE id=$3", deadline.Add(-api.InternalMailDomainCheckMaxAge), f.service.SendingIP.String(), f.domain.ID); err != nil {
					t.Fatal(err)
				}
			}
			f.delivery.Store(true)
			r, err := f.service.claimMail(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if expired == "lease" {
				if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_recipients SET lease_until=$1 WHERE id=$2", deadline, r.ID); err != nil {
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
				t.Fatal("handoff did not wait for the recipient-domain row lock", err)
			case <-time.After(30 * time.Millisecond):
			}
			waitMailTime(t, deadline)
			if err := lock.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				want := ErrLeaseLost
				if expired == "message" {
					want = ErrMessageExpired
				} else if expired == "authorization" {
					want = ErrUnverified
				}
				if !errors.Is(err, want) {
					t.Fatal("lock wait bypassed current deadline", err)
				}
			case <-ctx.Done():
				t.Fatal("blocked pacing reservation did not settle")
			}
			if !pacingSlot(t, f, "platform", "example.net").Equal(before) {
				t.Fatal("rejected submission committed a pacing reservation")
			}
			after, _ := f.store.Message(ctx, m.ID)
			if after.Recipients[0].Status != "claimed" {
				t.Fatal("rejected handoff committed recipient custody")
			}
		})
	}
}

func TestPGDomainPacingDoesNotRefundAmbiguousOrFailedHandoffs(t *testing.T) {
	for _, outcome := range []string{"uncertain", "deferred"} {
		t.Run(outcome, func(t *testing.T) {
			f := pacingFixture(t)
			first := singleMail(t, f, "first-handoff")
			singleMail(t, f, "second-handoff")
			f.transport.state, f.transport.code = outcome, "fixture_outcome"
			f.delivery.Store(true)
			if err := f.service.DeliverOne(t.Context()); err != nil {
				t.Fatal(err)
			}
			if outcome == "uncertain" {
				if err := f.store.Report(t.Context(), Report{EventID: uuid.NewString(), RecipientID: first.Recipients[0].ID, Kind: "retry", Code: "confirmed_not_accepted"}); err != nil {
					t.Fatal(err)
				}
			}
			f.service.Codec = nil
			if err := f.service.DeliverOne(t.Context()); err != nil || len(f.transport.envelopes) != 1 {
				t.Fatal("non-acceptance refunded a cooldown or decrypted blocked mail", err)
			}
		})
	}
}

func TestPGDomainPacingMigrationReplayAndRollbackGuard(t *testing.T) {
	f := pacingFixture(t)
	legacy := singleMail(t, f, "legacy-pacing-upgrade")
	raw, _ := migrations.FS.ReadFile("20261016110548882_internal_mail_domain_pacing.sql")
	parts := strings.Split(string(raw), "-- +goose StatementBegin\n")
	up := strings.Split(parts[1], "-- +goose StatementEnd")[0]
	down := strings.Split(parts[2], "-- +goose StatementEnd")[0]
	for range 2 {
		if _, err := f.pool.Exec(t.Context(), down); err != nil {
			t.Fatal(err)
		}
	}
	if f.store.Ready(t.Context()) == nil {
		t.Fatal("old schema passed pacing readiness")
	}
	for range 2 {
		if _, err := f.pool.Exec(t.Context(), up); err != nil {
			t.Fatal(err)
		}
	}
	if replay := singleMail(t, f, "legacy-pacing-upgrade"); replay.ID != legacy.ID {
		t.Fatal("pacing migration changed legacy replay identity")
	}
	f.delivery.Store(true)
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), down); err == nil {
		t.Fatal("rollback discarded durable cooldown")
	}
}
