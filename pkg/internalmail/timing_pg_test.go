package internalmail

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/internalmail/sqlc"
)

func timingFixture(t *testing.T) *pgFixture {
	t.Helper()
	f := producerFixture(t)
	f.service.SchedulingEnabled = func() bool { return true }
	return f
}

func timedRequest(send, expires *time.Time) SendRequest {
	r := producerRequest()
	r.MessageTiming = MessageTiming{SendAt: send, ExpiresAt: expires}
	return r
}

func waitMailTime(t *testing.T, at time.Time) {
	t.Helper()
	timer := time.NewTimer(time.Until(at) + 10*time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-t.Context().Done():
		t.Fatal("test cancelled while waiting for message time")
	}
}

func TestPGScheduledMailWaitsAcrossRestartWithoutClaims(t *testing.T) {
	f := timingFixture(t)
	sendAt := time.Now().Add(1200 * time.Millisecond)
	input := timedRequest(&sendAt, timeRef(sendAt.Add(time.Minute)))
	m, err := f.service.SubmitProduced(t.Context(), "auth", "scheduled", input)
	if err != nil || m.SendAt == nil || m.ExpiresAt == nil {
		t.Fatal("schedule admission failed", err)
	}
	queued, err := f.store.operatorMessage(t.Context(), m.ID)
	if err != nil || len(queued.Recipients) != 1 || !queued.Recipients[0].NextAttemptAt.Equal(*m.SendAt) {
		t.Fatal("scheduled queue priority does not match its earliest delivery time", err)
	}
	f.delivery.Store(true)
	codec := f.service.Codec
	f.service.Codec = nil
	for range 2 {
		if err := f.service.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	restarted, _ := NewPGStore(f.pool, "platform")
	f.service.Store = restarted
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	before, _ := f.store.Message(t.Context(), m.ID)
	if before.Recipients[0].Attempts != 0 || len(f.transport.envelopes) != 0 {
		t.Fatal("future mail was claimed or decrypted")
	}
	waitMailTime(t, sendAt)
	f.service.Codec = codec
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(f.transport.envelopes) != 1 || f.transport.envelopes[0].ExpiresAt == nil || !f.transport.envelopes[0].ExpiresAt.Equal(*m.ExpiresAt) {
		t.Fatal("due mail lost its deadline or was not submitted")
	}
}

func TestPGTimingAdmissionBoundsAndCanonicalReplay(t *testing.T) {
	f := timingFixture(t)
	for _, input := range []SendRequest{
		timedRequest(timeRef(time.Now().Add(-time.Minute)), nil),
		timedRequest(timeRef(time.Now().Add(api.InternalMailMaxScheduleDelay+time.Minute)), nil),
		timedRequest(nil, timeRef(time.Now().Add(-time.Minute))),
		timedRequest(nil, timeRef(time.Now().Add(api.InternalMailMaxQueueAge+time.Minute))),
	} {
		if _, err := f.service.Submit(t.Context(), uuid.NewString(), input); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid admission window accepted", err)
		}
	}
	input := timedRequest(timeRef(time.Now().Add(time.Hour)), timeRef(time.Now().Add(2*time.Hour)))
	m := submit(t, f, "canonical-times", input)
	offset := input
	offset.SendAt = timeRef(input.SendAt.In(time.FixedZone("caller", 10800)))
	offset.ExpiresAt = timeRef(input.ExpiresAt.In(time.FixedZone("caller", -14400)))
	if replay := submit(t, f, "canonical-times", offset); replay.ID != m.ID {
		t.Fatal("equivalent absolute times changed replay identity")
	}
	input.ExpiresAt = timeRef(input.ExpiresAt.Add(time.Minute))
	if _, err := f.service.Submit(t.Context(), "canonical-times", input); !errors.Is(err, ErrConflict) {
		t.Fatal("retry extended a persisted deadline", err)
	}
	input.MessageTiming = MessageTiming{}
	if _, err := f.service.Submit(t.Context(), "canonical-times", input); !errors.Is(err, ErrConflict) {
		t.Fatal("retry removed a persisted schedule", err)
	}
}

func TestPGTimingDisabledHoldsSavedWorkAndStillEnforcesExpiry(t *testing.T) {
	f := timingFixture(t)
	expires := time.Now().Add(800 * time.Millisecond)
	m := submit(t, f, "saved-deadline", timedRequest(nil, &expires))
	legacy := singleMail(t, f, "legacy-continues")
	f.service.SchedulingEnabled = func() bool { return false }
	f.delivery.Store(true)
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(f.transport.envelopes) != 1 || f.transport.envelopes[0].RecipientID != legacy.Recipients[0].ID {
		t.Fatal("timing disable blocked legacy work or dispatched timed mail")
	}
	f.service.Codec = nil
	waitMailTime(t, expires)
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	after, _ := f.store.Message(t.Context(), m.ID)
	if after.Recipients[0].Status != "expired" || after.Recipients[0].Attempts != 0 || len(f.transport.envelopes) != 1 {
		t.Fatal("disabled timing bypassed a saved deadline")
	}
}

func TestPGExpiryMaintenanceWithoutSMTPAndExpiredReplayAfterPruning(t *testing.T) {
	f := timingFixture(t)
	expires := time.Now().Add(500 * time.Millisecond)
	input := timedRequest(nil, &expires)
	m := submit(t, f, "expiry-replay", input)
	legacy := retentionMessage(t, f, "legacy-maintenance-scope", "queued")
	codec := f.service.Codec
	f.service.Codec = nil
	waitMailTime(t, expires)
	for range 2 {
		if err := f.service.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	after, _ := f.store.Message(t.Context(), m.ID)
	old, _ := f.store.Message(t.Context(), legacy.ID)
	if after.Recipients[0].Status != "expired" || old.Recipients[0].Status != "queued" || len(f.transport.envelopes) != 0 {
		t.Fatal("metadata-only expiry changed legacy work or attempted SMTP")
	}
	events, _ := f.store.Events(t.Context(), m.ID)
	count := 0
	for _, event := range events {
		if event.Kind == "expired" {
			count++
		}
	}
	if count != 1 {
		t.Fatal("expiry observation was not durable and deduplicated")
	}
	stats, err := f.store.Statistics(t.Context())
	if err != nil || stats.Recipients["expired"].Count != 1 || stats.Events["expired"] != 1 || stats.Outcomes["failed"] != 0 {
		t.Fatal("intentional expiry was counted as a delivery failure", err)
	}
	status, err := f.service.DeliveryStatus(t.Context(), DeliveryScope{Kind: "namespace"})
	if err != nil || status.Expired != 1 {
		t.Fatal("delivery status lost expired recipients", err)
	}
	f.service.Codec = codec
	if replay := submit(t, f, "expiry-replay", input); replay.ID != m.ID || replay.Recipients[0].Status != "expired" {
		t.Fatal("identical retry after expiry was rejected or revived")
	}
	if _, err := f.service.Submit(t.Context(), "new-expired-request", input); !errors.Is(err, ErrInvalid) {
		t.Fatal("new expired request inherited replay privileges", err)
	}
	if preview, err := f.service.RetainPayloads(t.Context(), true); err != nil || preview.EligibleMessages != 0 {
		t.Fatal("fresh expiry skipped retention quiet period", err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_messages SET created_at=now()-interval '8 days' WHERE id=$1", m.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_recipients SET updated_at=now()-interval '8 days' WHERE message_id=$1", m.ID); err != nil {
		t.Fatal(err)
	}
	if result, err := f.service.RetainPayloads(t.Context(), false); err != nil || result.PrunedMessages != 1 {
		t.Fatal("settled expiry could not be pruned", err)
	}
	restarted, _ := NewPGStore(f.pool, "platform")
	f.service.Store = restarted
	if replay := submit(t, f, "expiry-replay", input); replay.ID != m.ID || replay.PayloadPrunedAt == nil || replay.Recipients[0].Status != "expired" {
		t.Fatal("pruning lost absolute-time replay")
	}
	if err := f.store.Report(t.Context(), Report{EventID: uuid.NewString(), RecipientID: m.Recipients[0].ID, Kind: "retry", Code: "confirmed_not_accepted"}); !errors.Is(err, ErrConflict) {
		t.Fatal("terminal expired recipient was requeued", err)
	}
}

func TestPGScheduledAdmissionPreservesProducerBudgetsAndGateRollback(t *testing.T) {
	f := timingFixture(t)
	p := f.service.Producers["auth"]
	p.PendingRecipientLimit, p.DailyRecipientLimit = 1, 2
	f.service.Producers["auth"] = p
	input := timedRequest(timeRef(time.Now().Add(time.Hour)), nil)
	m, err := f.service.SubmitProduced(t.Context(), "auth", "timed-budget-first", input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.SubmitProduced(t.Context(), "auth", "timed-budget-second", input); !errors.Is(err, ErrQuota) {
		t.Fatal("future scheduling bypassed pending budget", err)
	}
	cancellation(t, f, m.ID, true)
	if _, err := f.service.SubmitProduced(t.Context(), "auth", "timed-budget-second", input); err != nil {
		t.Fatal("cancellation did not release pending capacity", err)
	}
	if _, err := f.service.SubmitProduced(t.Context(), "auth", "timed-budget-third", input); !errors.Is(err, ErrQuota) {
		t.Fatal("scheduling bypassed daily budget", err)
	}
	// Gate revocation after writes must roll the whole admission back.
	message := Message{ID: uuid.NewString(), DomainID: f.domain.ID, IdempotencyKey: "timing-revoked", SealedPayload: []byte("fixture"), MessageTiming: MessageTiming{ExpiresAt: timeRef(time.Now().Add(time.Hour).UTC().Truncate(time.Microsecond))}}
	recipient := Recipient{ID: uuid.NewString(), MessageID: message.ID, Address: "one@example.net"}
	calls := 0
	if _, _, err := f.store.enqueueTimed(t.Context(), message, []Recipient{recipient}, domainAuthorization{}, nil, func() bool { calls++; return calls == 1 }); !errors.Is(err, ErrDisabled) {
		t.Fatal("revoked timed admission committed", err)
	}
	if _, err := f.store.Message(t.Context(), message.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("revoked timed admission left a message", err)
	}
}

func TestPGPersistedTimingMustMatchEncryptedRequest(t *testing.T) {
	f := timingFixture(t)
	m := submit(t, f, "timing-integrity", timedRequest(nil, timeRef(time.Now().Add(time.Minute))))
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_messages SET expires_at=expires_at+interval '1 minute' WHERE id=$1", m.ID); err != nil {
		t.Fatal(err)
	}
	f.delivery.Store(true)
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	after, _ := f.store.Message(t.Context(), m.ID)
	if after.Recipients[0].Status != "failed" || after.Recipients[0].Code != "render_failed" || len(f.transport.envelopes) != 0 {
		t.Fatal("modified metadata silently extended the encrypted deadline")
	}
}

func TestPGDeadlineCrossingDuringRenderStopsSMTP(t *testing.T) {
	f := timingFixture(t)
	expires := time.Now().Add(700 * time.Millisecond)
	m := submit(t, f, "render-deadline", timedRequest(nil, &expires))
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
		t.Fatal("worker never entered rendering")
	}
	waitMailTime(t, expires)
	unblock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("expired worker did not settle")
	}
	after, _ := f.store.Message(t.Context(), m.ID)
	if after.Recipients[0].Status != "expired" || len(f.transport.envelopes) != 0 {
		t.Fatal("rendering crossed the handoff deadline")
	}
}

func TestPGDeadlineRecheckedAfterDomainLockWait(t *testing.T) {
	f := timingFixture(t)
	expires := time.Now().Add(800 * time.Millisecond)
	submit(t, f, "lock-deadline", timedRequest(nil, &expires))
	r, err := f.service.claimMail(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackOperator(t.Context(), tx)
	if _, err := f.store.queries.LockMailDomain(t.Context(), tx, q.LockMailDomainParams{Namespace: "platform", ID: pgID(f.domain.ID)}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- f.service.beginMailSubmission(ctx, r, f.domain.Selector) }()
	select {
	case err := <-done:
		t.Fatal("handoff did not wait for domain serialization", err)
	case <-time.After(30 * time.Millisecond):
	}
	waitMailTime(t, expires)
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrMessageExpired) {
			t.Fatal("transaction-start time bypassed deadline", err)
		}
	case <-ctx.Done():
		t.Fatal("handoff did not stop after lock release")
	}
}

func TestPGExpiryFencesAnActiveClaimAndStaleSettlement(t *testing.T) {
	f := timingFixture(t)
	expires := time.Now().Add(500 * time.Millisecond)
	m := submit(t, f, "active-expiry", timedRequest(nil, &expires))
	r, err := f.service.claimMail(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.BeginSubmission(t.Context(), r); !errors.Is(err, ErrDisabled) {
		t.Fatal("legacy store entrypoint bypassed timing access", err)
	}
	waitMailTime(t, expires)
	if err := f.store.expireTimed(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := f.service.settleMail(t.Context(), r, "queued", "delivery_disabled", time.Now()); err != nil {
		t.Fatal("expected expiry race reported a worker failure", err)
	}
	after, _ := f.store.Message(t.Context(), m.ID)
	if after.Recipients[0].Status != "expired" || after.Recipients[0].LeaseID != "" {
		t.Fatal("stale worker revived expired mail")
	}
}

func TestPGExpiryPreservesSubmittedAndUnknownCustody(t *testing.T) {
	f := timingFixture(t)
	expires := time.Now().Add(600 * time.Millisecond)
	first := submit(t, f, "relay-custody", timedRequest(nil, &expires))
	second := submit(t, f, "unknown-custody", timedRequest(nil, &expires))
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_recipients SET status='submitted' WHERE message_id=$1", first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_recipients SET status='uncertain' WHERE message_id=$1", second.ID); err != nil {
		t.Fatal(err)
	}
	waitMailTime(t, expires)
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	for id, status := range map[string]string{first.ID: "submitted", second.ID: "uncertain"} {
		m, _ := f.store.Message(t.Context(), id)
		if m.Recipients[0].Status != status {
			t.Fatal("expiry erased custody evidence", status)
		}
	}
	if err := f.store.Report(t.Context(), Report{EventID: uuid.NewString(), RecipientID: first.Recipients[0].ID, Kind: "accepted"}); err != nil {
		t.Fatal("late acceptance lost correlation", err)
	}
	if err := f.store.Report(t.Context(), Report{EventID: uuid.NewString(), RecipientID: second.Recipients[0].ID, Kind: "retry", Code: "confirmed_not_accepted"}); err != nil {
		t.Fatal(err)
	}
	f.delivery.Store(true)
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	m, _ := f.store.Message(t.Context(), second.ID)
	if m.Recipients[0].Status != "expired" || len(f.transport.envelopes) != 0 {
		t.Fatal("confirmed retry bypassed the original deadline")
	}
}

func TestPGTimingMetadataPrivacyMetricsAndFutureAge(t *testing.T) {
	f := timingFixture(t)
	input := timedRequest(timeRef(time.Now().Add(time.Hour)), timeRef(time.Now().Add(2*time.Hour)))
	m, err := f.service.SubmitProduced(t.Context(), "auth", "timing-metadata", input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_messages SET created_at=now()-interval '2 days' WHERE id=$1", m.ID); err != nil {
		t.Fatal(err)
	}
	f.service.Codec = nil
	owned, err := f.service.ProducerMessage(t.Context(), "auth", m.ID)
	if err != nil || owned.SendAt == nil || owned.ExpiresAt == nil {
		t.Fatal("owned timing metadata unavailable", err)
	}
	raw, _ := json.Marshal(owned)
	for _, secret := range []string{"sealed_payload", "123456", "fingerprint", "timing-metadata", "lease_id"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("timing inspection exposed content", secret)
		}
	}
	stats, err := f.store.Statistics(t.Context())
	if err != nil || stats.Recipients["queued"].OldestSeconds != 0 || stats.Recipients["queued"].StateAgeSeconds != 0 {
		t.Fatal("future mail appeared overdue", err)
	}
	inventory, err := f.store.SchedulingStatistics(t.Context())
	if err != nil || inventory.ScheduledWaiting != 1 || inventory.ExpiringPending != 1 {
		t.Fatal("timing aggregate inventory incorrect", err)
	}
	if _, err := f.service.ProducerMessage(t.Context(), "billing", m.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("timing metadata crossed producer ownership", err)
	}
	if result := cancellation(t, f, m.ID, true); !result.Complete {
		t.Fatal("future queued mail could not be cancelled")
	}
}

func TestPGSchedulingMigrationReplayAndRollbackGuard(t *testing.T) {
	f := timingFixture(t)
	legacy := singleMail(t, f, "legacy-schedule-upgrade")
	raw, _ := migrations.FS.ReadFile("20261016103033444_internal_mail_scheduling.sql")
	parts := strings.Split(string(raw), "-- +goose StatementBegin\n")
	up := strings.Split(parts[1], "-- +goose StatementEnd")[0]
	down := strings.Split(parts[2], "-- +goose StatementEnd")[0]
	for range 2 {
		if _, err := f.pool.Exec(t.Context(), down); err != nil {
			t.Fatal(err)
		}
	}
	if f.store.Ready(t.Context()) == nil {
		t.Fatal("old schema passed scheduling readiness")
	}
	for range 2 {
		if _, err := f.pool.Exec(t.Context(), up); err != nil {
			t.Fatal(err)
		}
	}
	if replay := singleMail(t, f, "legacy-schedule-upgrade"); replay.ID != legacy.ID {
		t.Fatal("migration changed legacy replay identity")
	}
	submit(t, f, "persisted-schedule", timedRequest(timeRef(time.Now().Add(time.Hour)), nil))
	if _, err := f.pool.Exec(t.Context(), down); err == nil {
		t.Fatal("rollback discarded a saved schedule")
	}
}
