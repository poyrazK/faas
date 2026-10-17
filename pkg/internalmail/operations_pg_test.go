package internalmail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/migrations"
)

func operationsFixture(t *testing.T) *pgFixture {
	t.Helper()
	f := retentionFixture(t)
	f.service.OperationsEnabled = func() bool { return true }
	f.service.OperationsToken = operatorToken
	return f
}

func TestPGOperatorReconcileReplayAndNoBlindResend(t *testing.T) {
	f := operationsFixture(t)
	m := retentionMessage(t, f, "operator-uncertain", "uncertain")
	input := ReconcileRequest{OperatorAuditInput: auditInput(), Outcome: "submitted", ConfirmedRelayAccepted: true}
	record, err := f.service.ReconcileUncertain(t.Context(), m.Recipients[0].ID, input)
	if err != nil || record.Resource != m.Recipients[0].ID || record.Actor != input.Actor || !bytes.Contains(record.Result, []byte("submitted")) {
		t.Fatal("reconciliation failed", err)
	}
	f.delivery.Store(true)
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(f.transport.envelopes) != 0 {
		t.Fatal("relay-owned message was resubmitted")
	}
	// The same request after restart must return the committed audit even if
	// a later delivery report has already advanced the recipient again.
	if err := f.store.Report(t.Context(), Report{EventID: uuid.NewString(), RecipientID: m.Recipients[0].ID, Kind: "accepted"}); err != nil {
		t.Fatal(err)
	}
	restarted, _ := NewPGStore(f.pool, "platform")
	f.service.Store = restarted
	replay, err := f.service.ReconcileUncertain(t.Context(), m.Recipients[0].ID, input)
	if err != nil || replay.ID != record.ID || !replay.CreatedAt.Equal(record.CreatedAt) || !replay.replayed {
		t.Fatal("lost-response replay changed operation", err)
	}
	input.Evidence = "ticket:different"
	if _, err := f.service.ReconcileUncertain(t.Context(), m.Recipients[0].ID, input); !errors.Is(err, ErrConflict) {
		t.Fatal("contradictory replay accepted", err)
	}
	input.OperatorAuditInput = auditInput()
	if _, err := f.service.ReconcileUncertain(t.Context(), m.Recipients[0].ID, input); !errors.Is(err, ErrConflict) {
		t.Fatal("terminal state overwritten", err)
	}
	page, err := restarted.operatorAudit(t.Context(), OperatorList{Limit: 25})
	if err != nil || len(page.Items) != 1 {
		t.Fatal("duplicate or missing audits", err)
	}
	events, err := f.store.Events(t.Context(), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, e := range events {
		if e.ID == "operator:"+record.ID && e.Kind == "submitted" {
			count++
		}
	}
	if count != 1 {
		t.Fatal("missing delivery audit event")
	}
}

func TestPGOperatorRetryFencesActiveLeasesAndPreservesSafeguards(t *testing.T) {
	f := operationsFixture(t)
	m := submit(t, f, "operator-retry", request())
	claimed, err := f.store.Claim(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.BeginSubmission(t.Context(), claimed); err != nil {
		t.Fatal(err)
	}
	input := ReconcileRequest{OperatorAuditInput: auditInput(), Outcome: "retry", ConfirmedNotAccepted: true}
	if _, err := f.service.ReconcileUncertain(t.Context(), claimed.ID, input); !errors.Is(err, ErrConflict) {
		t.Fatal("active submission unfenced", err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_recipients SET lease_until=now()-interval '1 second' WHERE id=$1", claimed.ID); err != nil {
		t.Fatal(err)
	}
	record, err := f.service.ReconcileUncertain(t.Context(), claimed.ID, input)
	if err != nil || !bytes.Contains(record.Result, []byte("queued")) {
		t.Fatal(err)
	}
	if err := f.store.Settle(t.Context(), claimed, "submitted", "late", f.service.now()); !errors.Is(err, ErrLeaseLost) {
		t.Fatal("old submission regained authority", err)
	}
	after, err := f.store.Message(t.Context(), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range after.Recipients {
		if r.ID == claimed.ID && (r.Status != "queued" || r.Attempts != 1) {
			t.Fatal("retry reset safeguards")
		}
	}
	// Explicit retry does not bypass the pilot allowlist at dispatch.
	f.delivery.Store(true)
	f.service.RecipientAllowed = func(string) bool { return false }
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(f.transport.envelopes) != 0 {
		t.Fatal("operator retry bypassed pilot")
	}
}

func TestPGOperatorReadsAreBoundedScopedAndExcludeSecrets(t *testing.T) {
	f := operationsFixture(t)
	m := retentionMessage(t, f, "do-not-expose-idempotency-key", "uncertain")
	retentionMessage(t, f, "operator-second", "accepted")
	other, _ := NewPGStore(f.pool, "other")
	f.service.Store = other
	for _, read := range []func() error{
		func() error {
			p, e := other.operatorMessages(t.Context(), OperatorList{Limit: 1})
			if len(p.Items) != 0 {
				t.Fatal("namespace message leak")
			}
			return e
		},
		func() error {
			p, e := other.operatorDomains(t.Context(), OperatorList{Limit: 1})
			if len(p.Items) != 0 {
				t.Fatal("namespace domain leak")
			}
			return e
		},
	} {
		if err := read(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.service.ReconcileUncertain(t.Context(), m.Recipients[0].ID, ReconcileRequest{OperatorAuditInput: auditInput(), Outcome: "retry", ConfirmedNotAccepted: true}); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-namespace mutation", err)
	}
	if _, err := other.operatorMessage(t.Context(), m.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	f.service.Store = f.store
	first, err := f.store.operatorMessages(t.Context(), OperatorList{Limit: 1})
	if err != nil || len(first.Items) != 1 || first.Next == "" {
		t.Fatal("unbounded page", err)
	}
	second, err := f.store.operatorMessages(t.Context(), OperatorList{Limit: 1, After: first.Next})
	if err != nil || len(second.Items) != 1 || second.Next != "" || second.Items[0].ID == first.Items[0].ID {
		t.Fatal("cursor repeated or skipped", err)
	}
	filtered, err := f.store.operatorMessages(t.Context(), OperatorList{Limit: 1, Status: "uncertain"})
	if err != nil || len(filtered.Items) != 1 || filtered.Items[0].ID != m.ID {
		t.Fatal("state filter failed", err)
	}
	if err := f.store.Report(t.Context(), Report{EventID: uuid.NewString(), RecipientID: m.Recipients[0].ID, Kind: "complained"}); err != nil {
		t.Fatal(err)
	}
	h, err := Handler(f.service, testToken)
	if err != nil {
		t.Fatal(err)
	}
	f.service.Codec = nil // Inspection must not need payload/signing key access.
	for _, path := range []string{"/messages", "/messages/" + m.ID, "/messages/" + m.ID + "/events?limit=1", "/domains", "/domains/" + f.domain.ID, "/domains/" + f.domain.ID + "/check", "/suppressions", "/audit"} {
		r := httptest.NewRequest(http.MethodGet, OperationsPath+path, nil)
		r.Header.Set("Authorization", "Bearer "+operatorToken)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal("operator read failed", path, w.Code)
		}
		for _, secret := range []string{"sealed_payload", "sealed_key", "fingerprint", "idempotency_key", "lease_id", "do-not-expose-idempotency-key", "123456"} {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatal("private data exposed", secret)
			}
		}
	}
	p, err := f.store.operatorSuppressions(t.Context(), OperatorList{Limit: 1})
	if err != nil || len(p.Items) != 1 || p.Items[0].Reason != "complained" {
		t.Fatal("suppression review failed", err)
	}
	delete(f.resolver, "_dmarc."+f.domain.Name)
	checked := f.service.checkOperatorDNS(t.Context(), f.domain.Domain)
	if checked.DNSReady || checked.Records[3].Status != "lookup_failed" {
		t.Fatal("missing DNS passed")
	}
	d, _ := f.store.Domain(t.Context(), f.domain.ID)
	if d.Status != "verified" {
		t.Fatal("DNS observation mutated domain")
	}
}

func TestPGOperatorRetentionAuditIsAtomicAndReplaySafe(t *testing.T) {
	f := operationsFixture(t)
	m := retentionMessage(t, f, "operator-cleanup", "accepted")
	input := auditInput()
	f.service.RetentionPolicy.DryRun = true
	if _, err := f.service.OperatorRetainPayloads(t.Context(), input); !errors.Is(err, ErrRetentionDryRun) {
		t.Fatal("preview-only policy overridden", err)
	}
	f.service.RetentionPolicy.DryRun = false
	record, err := f.service.OperatorRetainPayloads(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	var result RetentionResult
	if json.Unmarshal(record.Result, &result) != nil || result.PrunedMessages != 1 {
		t.Fatal("audit omitted cleanup result")
	}
	after, _ := f.store.Message(t.Context(), m.ID)
	if after.PayloadPrunedAt == nil {
		t.Fatal("cleanup did not commit")
	}
	retentionMessage(t, f, "operator-cleanup-next", "accepted")
	f.service.RetentionPolicy.BatchSize = 1
	replay, err := f.service.OperatorRetainPayloads(t.Context(), input)
	if err != nil || replay.ID != record.ID || !replay.replayed {
		t.Fatal("cleanup replay ran again", err)
	}
	page, _ := f.store.operatorAudit(t.Context(), OperatorList{Limit: 25})
	if len(page.Items) != 1 {
		t.Fatal("cleanup audit duplicated")
	}
	// Revoking operations during the cleanup rolls back both the body update
	// and audit. A cancelled request likewise leaves no half-committed action.
	for _, cancelled := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		enabled := true
		_, err := f.store.operatorRetention(ctx, auditInput(), f.service.RetentionPolicy, func(m Message) ([]byte, error) {
			fp, err := f.service.retentionFingerprint(m)
			if cancelled {
				cancel()
			} else {
				enabled = false
			}
			return fp, err
		}, func() bool { return enabled })
		cancel()
		if err == nil {
			t.Fatal("revoked/cancelled cleanup committed")
		}
	}
	page, _ = f.store.operatorAudit(t.Context(), OperatorList{Limit: 25})
	if len(page.Items) != 1 {
		t.Fatal("failed cleanup left success audit")
	}
}

func TestPGOperatorConcurrentRequestAndAuditFailureRollback(t *testing.T) {
	f := operationsFixture(t)
	m := retentionMessage(t, f, "operator-concurrent", "uncertain")
	input := ReconcileRequest{OperatorAuditInput: auditInput(), Outcome: "retry", ConfirmedNotAccepted: true}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			if _, err := f.service.ReconcileUncertain(t.Context(), m.Recipients[0].ID, input); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	page, _ := f.store.operatorAudit(t.Context(), OperatorList{Limit: 25})
	if len(page.Items) != 1 {
		t.Fatal("concurrent replay not serialized")
	}
	second := retentionMessage(t, f, "operator-audit-failure", "uncertain")
	if _, err := f.pool.Exec(t.Context(), `CREATE FUNCTION reject_operator_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'audit unavailable'; END $$;
CREATE TRIGGER reject_operator_audit BEFORE INSERT ON internal_mail_operations FOR EACH ROW EXECUTE FUNCTION reject_operator_audit()`); err != nil {
		t.Fatal(err)
	}
	input.OperatorAuditInput = auditInput()
	if _, err := f.service.ReconcileUncertain(t.Context(), second.Recipients[0].ID, input); err == nil {
		t.Fatal("mutation succeeded without audit")
	}
	after, _ := f.store.Message(t.Context(), second.ID)
	if after.Recipients[0].Status != "uncertain" {
		t.Fatal("audit failure did not roll back mutation")
	}
	// The same guarantee also covers deletion, not only recipient transitions.
	settled := retentionMessage(t, f, "operator-audit-prune-failure", "accepted")
	if _, err := f.service.OperatorRetainPayloads(t.Context(), auditInput()); err == nil {
		t.Fatal("pruning succeeded without audit")
	}
	assertPayloadRetained(t, f, settled.ID)
}

func TestPGOperatorMigrationPreservesAudit(t *testing.T) {
	f := operationsFixture(t)
	raw, _ := migrations.FS.ReadFile("20261015154854536_internal_mail_operations_audit.sql")
	parts := strings.Split(string(raw), "-- +goose StatementBegin\n")
	up := strings.Split(parts[1], "-- +goose StatementEnd")[0]
	down := strings.Split(parts[2], "-- +goose StatementEnd")[0]
	for range 2 {
		if _, err := f.pool.Exec(t.Context(), down); err != nil {
			t.Fatal(err)
		}
	}
	if f.store.Ready(t.Context()) == nil {
		t.Fatal("missing audit schema reported ready")
	}
	for range 2 {
		if _, err := f.pool.Exec(t.Context(), up); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.service.OperatorRetainPayloads(t.Context(), auditInput()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), down); err == nil {
		t.Fatal("rollback discarded operator audit")
	}
	if f.store.Ready(t.Context()) != nil {
		t.Fatal("refused rollback damaged schema")
	}
}
