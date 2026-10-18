package internalmail

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/migrations"
	q "github.com/onebox-faas/faas/pkg/internalmail/sqlc"
)

func controlsFixture(t *testing.T) *pgFixture {
	t.Helper()
	f := operationsFixture(t)
	f.service.DeliveryControlsEnabled = func() bool { return true }
	return f
}
func controlRequest(scope DeliveryScope, revision int64) DeliveryControlRequest {
	return DeliveryControlRequest{OperatorAuditInput: auditInput(), DeliveryScope: scope, ExpectedRevision: &revision}
}
func controlChange(t *testing.T, f *pgFixture, scope DeliveryScope, mode string, revision int64) DeliveryControlStatus {
	t.Helper()
	record, err := f.service.ChangeDeliveryControl(t.Context(), mode, controlRequest(scope, revision))
	if err != nil {
		t.Fatal(err)
	}
	var status DeliveryControlStatus
	if json.Unmarshal(record.Result, &status) != nil {
		t.Fatal("invalid control status")
	}
	return status
}
func cancellation(t *testing.T, f *pgFixture, id string, message bool) CancellationResult {
	t.Helper()
	record, err := f.service.CancelMail(t.Context(), id, message, auditInput())
	if err != nil {
		t.Fatal(err)
	}
	var result CancellationResult
	if json.Unmarshal(record.Result, &result) != nil {
		t.Fatal("invalid cancellation result")
	}
	return result
}
func singleMail(t *testing.T, f *pgFixture, key string) MessageResponse {
	t.Helper()
	r := request()
	r.To = []string{"one@example.net"}
	return submit(t, f, key, r)
}

func TestPGDeliveryControlPersistenceCASAndReplay(t *testing.T) {
	f := controlsFixture(t)
	scope := DeliveryScope{Kind: "namespace"}
	m := singleMail(t, f, "paused-work")
	status, err := f.service.DeliveryStatus(t.Context(), scope)
	if err != nil || status.Mode != "running" || status.Revision != 0 || status.Held {
		t.Fatal("initial control was not running", err)
	}
	input := controlRequest(scope, 0)
	record, err := f.service.ChangeDeliveryControl(t.Context(), "paused", input)
	if err != nil {
		t.Fatal(err)
	}
	f.service.Codec = nil // Persisted holds must prevent message/key decryption.
	f.delivery.Store(true)
	for range 3 {
		if err := f.service.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	after, _ := f.store.Message(t.Context(), m.ID)
	if after.Recipients[0].Attempts != 0 || after.Recipients[0].Status != "queued" || len(f.transport.envelopes) != 0 {
		t.Fatal("paused dispatch claimed work")
	}
	restarted, _ := NewPGStore(f.pool, "platform")
	f.service.Store = restarted
	f.service.DeliveryControlsEnabled = func() bool { return false }
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(f.transport.envelopes) != 0 {
		t.Fatal("disabling access resumed persisted hold")
	}
	f.service.DeliveryControlsEnabled = func() bool { return true }
	if _, err := f.service.ChangeDeliveryControl(t.Context(), "running", controlRequest(scope, 0)); !errors.Is(err, ErrControlRevision) {
		t.Fatal("stale resume accepted", err)
	}
	controlChange(t, f, scope, "running", 1)
	if replay, err := f.service.ChangeDeliveryControl(t.Context(), "paused", input); err != nil || replay.ID != record.ID {
		t.Fatal("lost-response replay changed intent", err)
	}
	status, err = f.service.DeliveryStatus(t.Context(), scope)
	if err != nil || status.Mode != "running" || status.Revision != 2 {
		t.Fatal("old pause replay undid a newer resume", err)
	}
	input.Reason = "changed"
	if _, err := f.service.ChangeDeliveryControl(t.Context(), "paused", input); !errors.Is(err, ErrConflict) {
		t.Fatal("contradictory replay accepted", err)
	}
}

func TestPGDeliveryControlsNestedScopesAndUnrelatedDomains(t *testing.T) {
	f := controlsFixture(t)
	first := singleMail(t, f, "first-domain")
	secondDomain, err := f.service.RegisterDomain(t.Context(), "second.test")
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range secondDomain.Records {
		f.resolver[record.Name] = []string{record.Value}
	}
	if _, err := f.service.VerifyDomain(t.Context(), secondDomain.ID); err != nil {
		t.Fatal(err)
	}
	r := request()
	r.From = "sender@second.test"
	r.To = []string{"one@example.net"}
	second := submit(t, f, "second-domain", r)
	domainScope := DeliveryScope{Kind: "domain", DomainID: f.domain.ID}
	ns := DeliveryScope{Kind: "namespace"}
	controlChange(t, f, domainScope, "paused", 0)
	f.delivery.Store(true)
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(f.transport.envelopes) != 1 || f.transport.envelopes[0].RecipientID != second.Recipients[0].ID {
		t.Fatal("domain hold blocked unrelated delivery")
	}
	controlChange(t, f, ns, "paused", 0)
	status := controlChange(t, f, domainScope, "running", 1)
	if !status.Held || status.NamespaceMode != "paused" {
		t.Fatal("domain resume bypassed namespace hold")
	}
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	after, _ := f.store.Message(t.Context(), first.ID)
	if after.Recipients[0].Attempts != 0 {
		t.Fatal("nested hold consumed attempts")
	}
	controlChange(t, f, ns, "running", 1)
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(f.transport.envelopes) != 2 {
		t.Fatal("resume did not restore dispatch")
	}
	other, _ := NewPGStore(f.pool, "other")
	f.service.Store = other
	if _, err := f.service.DeliveryStatus(t.Context(), domainScope); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-namespace control read", err)
	}
	if _, err := f.service.ChangeDeliveryControl(t.Context(), "paused", controlRequest(domainScope, 0)); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-namespace control write", err)
	}
}

func TestPGPauseFencesClaimedWorkAndRefundsUnsubmittedAttempt(t *testing.T) {
	f := controlsFixture(t)
	m := singleMail(t, f, "claim-before-pause")
	r, err := f.store.Claim(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	controlChange(t, f, DeliveryScope{Kind: "namespace"}, "paused", 0)
	if err := f.store.beginSignedSubmission(t.Context(), r, f.domain.Selector, domainAuthorization{}); !errors.Is(err, ErrDeliveryPaused) {
		t.Fatal("claim before hold crossed handoff", err)
	}
	f.service.Codec = nil
	f.delivery.Store(true)
	state, code := f.service.prepareAndSubmit(t.Context(), r)
	if state != "queued" || code != "delivery_paused" {
		t.Fatal("held claim tried rendering")
	}
	if err := f.service.settleMail(t.Context(), r, state, code, time.Now()); err != nil {
		t.Fatal(err)
	}
	after, _ := f.store.Message(t.Context(), m.ID)
	if after.Recipients[0].Attempts != 0 || after.Recipients[0].LeaseID != "" {
		t.Fatal("pause charged an unsubmitted attempt")
	}
}

func TestPGPauseSerializesWithFirstHandoff(t *testing.T) {
	for _, kind := range []string{"namespace", "domain"} {
		t.Run(kind, func(t *testing.T) {
			f := controlsFixture(t)
			singleMail(t, f, "handoff-lock")
			r, err := f.store.Claim(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			tx, err := f.pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer rollbackOperator(t.Context(), tx)
			if err := f.store.shareSubmissionControls(t.Context(), tx, r.ID); err != nil {
				t.Fatal(err)
			}
			scope := DeliveryScope{Kind: kind}
			if kind == "domain" {
				scope.DomainID = f.domain.ID
			}
			done := make(chan error, 1)
			go func() {
				_, err := f.service.ChangeDeliveryControl(t.Context(), "paused", controlRequest(scope, 0))
				done <- err
			}()
			select {
			case err := <-done:
				t.Fatal("pause committed while an older handoff held its fence", err)
			case <-time.After(50 * time.Millisecond):
			}
			n, err := f.store.queries.BeginSubmission(t.Context(), tx, q.BeginSubmissionParams{Namespace: "platform", ID: pgID(r.ID), LeaseID: pgID(r.LeaseID), ExpectedSelector: f.domain.Selector})
			if err != nil || n != 1 {
				t.Fatal("earlier handoff could not finish", err)
			}
			if err := tx.Commit(t.Context()); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("pause did not complete after handoff")
			}
			result := cancellation(t, f, r.ID, false)
			if result.Complete || len(result.Protected) != 1 || result.Protected[0].Status != "submitting" {
				t.Fatal("pause revoked an earlier submitting attempt")
			}
		})
	}
}

func TestPGCancellationStateMatrixAndNamespace(t *testing.T) {
	f := controlsFixture(t)
	for _, state := range []string{"queued", "deferred", "claimed", "submitting", "submitted", "uncertain", "accepted", "bounced", "complained", "suppressed", "failed", "cancelled"} {
		m := retentionMessage(t, f, "cancel-"+state, state)
		result := cancellation(t, f, m.ID, true)
		eligible := state == "queued" || state == "deferred" || state == "claimed" || state == "cancelled"
		if result.Complete != eligible {
			t.Fatal("incorrect cancellation ownership", state, result)
		}
		after, _ := f.store.Message(t.Context(), m.ID)
		if eligible {
			if after.Recipients[0].Status != "cancelled" || after.Recipients[0].LeaseID != "" {
				t.Fatal("cancelled recipient retained worker lease")
			}
		} else if after.Recipients[0].Status != state {
			t.Fatal("relay/terminal state overwritten", state)
		}
	}
	other, _ := NewPGStore(f.pool, "other")
	m := singleMail(t, f, "cross-cancel")
	f.service.Store = other
	if _, err := f.service.CancelMail(t.Context(), m.ID, true, auditInput()); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-namespace cancellation", err)
	}
}

func TestPGCancellationReplayAtomicityAndNoResurrection(t *testing.T) {
	f := controlsFixture(t)
	m := submit(t, f, "cancel-replay", request())
	input := auditInput()
	record, err := f.service.CancelMail(t.Context(), m.ID, true, input)
	if err != nil {
		t.Fatal(err)
	}
	restarted, _ := NewPGStore(f.pool, "platform")
	f.service.Store = restarted
	if replay, err := f.service.CancelMail(t.Context(), m.ID, true, input); err != nil || replay.ID != record.ID {
		t.Fatal("cancellation replay changed result", err)
	}
	if replay, err := f.service.Submit(t.Context(), "cancel-replay", request()); err != nil || replay.ID != m.ID || replay.Recipients[0].Status != "cancelled" {
		t.Fatal("idempotent send resurrected cancelled work", err)
	}
	f.delivery.Store(true)
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(f.transport.envelopes) != 0 {
		t.Fatal("cancelled work reached SMTP")
	}
	events, _ := f.store.Events(t.Context(), m.ID)
	count := 0
	for _, event := range events {
		if event.Kind == "cancelled" {
			count++
		}
	}
	if count != 2 {
		t.Fatal("cancellation events duplicated or missing")
	}
	if err := f.store.Report(t.Context(), Report{EventID: uuid.NewString(), RecipientID: m.Recipients[0].ID, Kind: "accepted"}); !errors.Is(err, ErrConflict) {
		t.Fatal("report changed cancelled intent", err)
	}
	if err := f.store.Report(t.Context(), Report{EventID: uuid.NewString(), RecipientID: m.Recipients[0].ID, Kind: "retry", Code: "confirmed_not_accepted"}); !errors.Is(err, ErrConflict) {
		t.Fatal("manual retry resurrected cancelled work", err)
	}
	stats, err := f.store.Statistics(t.Context())
	if err != nil || stats.Recipients["cancelled"].Count != 2 {
		t.Fatal("cancellation missing from durable monitoring", err)
	}
}

func TestPGControlAuditFailureRevocationAndCancellationRollback(t *testing.T) {
	f := controlsFixture(t)
	m := singleMail(t, f, "audit-rollback")
	scope := DeliveryScope{Kind: "namespace"}
	for _, cancelled := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		var calls atomic.Int32
		enabled := func() bool {
			if calls.Add(1) > 1 {
				if cancelled {
					cancel()
					return true
				}
				return false
			}
			return true
		}
		if _, err := f.store.changeDeliveryControl(ctx, "paused", controlRequest(scope, 0), enabled); err == nil {
			t.Fatal("revoked/cancelled pause committed")
		}
		cancel()
		status, _ := f.service.DeliveryStatus(t.Context(), scope)
		if status.Revision != 0 || status.Held {
			t.Fatal("failed pause changed scope")
		}
	}
	if _, err := f.pool.Exec(t.Context(), `CREATE FUNCTION reject_control_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'audit unavailable'; END $$;
CREATE TRIGGER reject_control_audit BEFORE INSERT ON internal_mail_operations FOR EACH ROW EXECUTE FUNCTION reject_control_audit()`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.ChangeDeliveryControl(t.Context(), "paused", controlRequest(scope, 0)); err == nil {
		t.Fatal("pause without durable audit")
	}
	if _, err := f.service.CancelMail(t.Context(), m.ID, true, auditInput()); err == nil {
		t.Fatal("cancellation without durable audit")
	}
	after, _ := f.store.Message(t.Context(), m.ID)
	if after.Recipients[0].Status != "queued" {
		t.Fatal("audit failure did not roll back cancellation")
	}
	events, _ := f.store.Events(t.Context(), m.ID)
	for _, event := range events {
		if event.Kind == "cancelled" {
			t.Fatal("failed cancellation left an event")
		}
	}
}

func TestPGDrainStatusDistinguishesRelayAndUnknownCustody(t *testing.T) {
	f := controlsFixture(t)
	scope := DeliveryScope{Kind: "namespace"}
	claimed := retentionMessage(t, f, "active-claim", "claimed")
	submitting := retentionMessage(t, f, "active-submit", "submitting")
	retentionMessage(t, f, "relay-owned", "submitted")
	controlChange(t, f, scope, "draining", 0)
	status, err := f.service.DeliveryStatus(t.Context(), scope)
	if err != nil || status.Drained || status.ActiveClaims != 1 || status.ActiveSubmissions != 1 || status.RelayOwned != 1 {
		t.Fatal("in-flight work hidden from drain", status, err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_recipients SET lease_until=now()-interval '1 second' WHERE message_id IN ($1,$2)", claimed.ID, submitting.ID); err != nil {
		t.Fatal(err)
	}
	status, err = f.service.DeliveryStatus(t.Context(), scope)
	if err != nil || status.Drained || status.ExpiredClaims != 1 || status.Uncertain != 1 {
		t.Fatal("expired submitting lease looked drained", status, err)
	}
	if err := f.store.Report(t.Context(), Report{EventID: uuid.NewString(), RecipientID: submitting.Recipients[0].ID, Kind: "accepted"}); err != nil {
		t.Fatal(err)
	}
	status, err = f.service.DeliveryStatus(t.Context(), scope)
	if err != nil || !status.Drained || status.RelayOwned != 1 {
		t.Fatal("drain confused local completion with downstream delivery", status, err)
	}
}

func TestPGCancelledRetentionQuietPeriodAndBounds(t *testing.T) {
	f := controlsFixture(t)
	m := retentionMessage(t, f, "cancelled-retention", "queued")
	cancellation(t, f, m.ID, true)
	preview, err := f.service.RetainPayloads(t.Context(), true)
	if err != nil || preview.EligibleMessages != 0 {
		t.Fatal("fresh cancellation skipped retention quiet period", err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_recipients SET updated_at=now()-interval '8 days' WHERE message_id=$1", m.ID); err != nil {
		t.Fatal(err)
	}
	result, err := f.service.RetainPayloads(t.Context(), false)
	if err != nil || result.PrunedMessages != 1 {
		t.Fatal("settled cancellation never became eligible", err)
	}
	after, _ := f.store.Message(t.Context(), m.ID)
	if after.Recipients[0].Status != "cancelled" || after.PayloadPrunedAt == nil {
		t.Fatal("retention lost cancellation metadata")
	}
	input := request()
	input.To = []string{"one@example.net"}
	if replay, err := f.service.Submit(t.Context(), "cancelled-retention", input); err != nil || replay.ID != m.ID || replay.Recipients[0].Status != "cancelled" {
		t.Fatal("payload pruning lost cancelled-message idempotency", err)
	}
	large := singleMail(t, f, "oversized-cancel")
	for i := range 10 {
		if _, err := f.pool.Exec(t.Context(), "INSERT INTO internal_mail_recipients(id,namespace,message_id,address) VALUES($1,'platform',$2,$3)", uuid.NewString(), large.ID, uuid.NewString()+"@example.net"); err != nil {
			t.Fatal(i, err)
		}
	}
	if _, err := f.service.CancelMail(t.Context(), large.ID, true, auditInput()); !errors.Is(err, ErrQuota) {
		t.Fatal("unbounded cancellation accepted", err)
	}
	after, _ = f.store.Message(t.Context(), large.ID)
	for _, r := range after.Recipients {
		if r.Status != "queued" {
			t.Fatal("oversized request partially cancelled mail")
		}
	}
}

func TestPGDeliveryControlsMigrationReplayAndGuards(t *testing.T) {
	f := controlsFixture(t)
	raw, _ := migrations.FS.ReadFile("20261016085426393_internal_mail_delivery_controls.sql")
	parts := strings.Split(string(raw), "-- +goose StatementBegin\n")
	up := strings.Split(parts[1], "-- +goose StatementEnd")[0]
	down := strings.Split(parts[2], "-- +goose StatementEnd")[0]
	for range 2 {
		if _, err := f.pool.Exec(t.Context(), down); err != nil {
			t.Fatal(err)
		}
	}
	if f.store.Ready(t.Context()) == nil {
		t.Fatal("old schema passed readiness")
	}
	for range 2 {
		if _, err := f.pool.Exec(t.Context(), up); err != nil {
			t.Fatal(err)
		}
	}
	controlChange(t, f, DeliveryScope{Kind: "namespace"}, "paused", 0)
	if _, err := f.pool.Exec(t.Context(), down); err == nil {
		t.Fatal("rollback removed a persisted hold")
	}
	controlChange(t, f, DeliveryScope{Kind: "namespace"}, "running", 1)
	if _, err := f.pool.Exec(t.Context(), down); err == nil {
		t.Fatal("rollback removed operator evidence")
	}
}
