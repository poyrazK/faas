package internalmail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/migrations"
)

func recoveryFixture(t *testing.T) *pgFixture {
	t.Helper()
	f := operationsFixture(t)
	legacy := f.service.Codec.(AgeCodec).Identity
	f.service.Codec = keyringForTest(t, legacy, legacy)
	f.service.RecoveryEnabled = func() bool { return true }
	return f
}

func startRecovery(t *testing.T, f *pgFixture) RecoveryStatus {
	t.Helper()
	if err := f.store.ConfigureRecovery(t.Context(), uuid.NewString(), f.service.recoveryEnabled); err != nil {
		t.Fatal(err)
	}
	status, err := f.service.RecoveryStatus(t.Context())
	if err != nil || !status.Held {
		t.Fatal("missing recovery hold", err)
	}
	return status
}

func recoveryReview(status RecoveryStatus, expected, outcome string) RecoveryReviewRequest {
	return RecoveryReviewRequest{ReconcileRequest: ReconcileRequest{OperatorAuditInput: auditInput(), Outcome: outcome, ConfirmedNotAccepted: outcome == "retry", ConfirmedRelayAccepted: outcome == "submitted"}, RecoveryID: status.RecoveryID, ExpectedRevision: &status.Revision, ExpectedStatus: expected}
}

func recoveryRelease(status RecoveryStatus) RecoveryReleaseRequest {
	return RecoveryReleaseRequest{OperatorAuditInput: auditInput(), RecoveryID: status.RecoveryID, ExpectedRevision: &status.Revision, ConfirmedPolicy: true, ConfirmedAccess: true, ConfirmedConsumers: true, ConfirmedRelay: true, ConfirmedQuarantine: true}
}

// This is an offline restore drill: take a recipient snapshot, hand mail to a
// fake relay, rewind database custody to that snapshot, then start a new daemon
// store with a fresh externally supplied restore ID. Relay evidence survives
// the rewind; it must not produce a second submission.
func TestPGRecoveryOfflineRestoreDrill(t *testing.T) {
	f := recoveryFixture(t)
	sent := singleMail(t, f, "before-backup")
	if _, err := f.pool.Exec(t.Context(), "CREATE TABLE recovery_drill_snapshot AS SELECT * FROM internal_mail_recipients"); err != nil {
		t.Fatal(err)
	}
	f.delivery.Store(true)
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(f.transport.envelopes) != 1 {
		t.Fatal("relay did not accept before rewind")
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE internal_mail_recipients r SET status=b.status,attempts=b.attempts,
        code=b.code,lease_id=b.lease_id,lease_until=b.lease_until,updated_at=b.updated_at,
        signing_selector=b.signing_selector,recovery_id=b.recovery_id FROM recovery_drill_snapshot b WHERE r.id=b.id;
        DELETE FROM internal_mail_events WHERE kind<>'queued'`); err != nil {
		t.Fatal(err)
	}
	unknown := singleMail(t, f, "unknown-after-restore")
	retry := singleMail(t, f, "evidence-proves-no-relay-acceptance")
	restarted, err := NewPGStore(f.pool, "platform")
	if err != nil {
		t.Fatal(err)
	}
	f.store, f.service.Store = restarted, restarted
	status := startRecovery(t, f)
	if status.Quarantined != 3 {
		t.Fatal("snapshot queue escaped quarantine", status)
	}
	if err := f.service.DeliverOne(t.Context()); err != nil || len(f.transport.envelopes) != 1 {
		t.Fatal("held worker resubmitted restored message", err)
	}
	if _, err := f.service.Submit(t.Context(), "held-new-mail", request()); !errors.Is(err, ErrRecoveryHeld) {
		t.Fatal("recovery admitted new mail", err)
	}
	preview, err := f.service.PreviewRecovery(t.Context(), OperatorList{Limit: 1})
	if err != nil || len(preview.Items) != 1 || preview.Next == "" || preview.RecoveryID != status.RecoveryID {
		t.Fatal("unbounded or unversioned preview", err)
	}
	next, err := f.service.PreviewRecovery(t.Context(), OperatorList{Limit: 2, After: preview.Next})
	if err != nil || len(next.Items) != 2 || next.Next != "" {
		t.Fatal("preview cursor skipped restored mail", err)
	}
	raw, _ := json.Marshal(preview)
	if bytes.Contains(raw, []byte("123456")) || bytes.Contains(raw, []byte("one@example")) || bytes.Contains(raw, []byte("sealed")) {
		t.Fatal("preview disclosed private content")
	}
	sentInput := recoveryReview(status, "queued", "submitted")
	saved, err := f.service.ReviewRecovery(t.Context(), sent.Recipients[0].ID, sentInput)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.Report(t.Context(), Report{EventID: "late-real-relay-report", RecipientID: sent.Recipients[0].ID, Kind: "accepted"}); err != nil {
		t.Fatal(err)
	}
	replay, err := f.service.ReviewRecovery(t.Context(), sent.Recipients[0].ID, sentInput)
	if err != nil || !replay.replayed || !bytes.Equal(replay.Result, saved.Result) {
		t.Fatal("lost-response review changed later custody", err)
	}
	if _, err := f.service.ReviewRecovery(t.Context(), retry.Recipients[0].ID, recoveryReview(status, "queued", "retry")); err != nil {
		t.Fatal(err)
	}
	release := recoveryRelease(status)
	if _, err := f.service.ReleaseRecovery(t.Context(), release); err != nil {
		t.Fatal(err)
	}
	status, err = f.service.RecoveryStatus(t.Context())
	if err != nil || status.Held || status.Revision != 2 || status.Quarantined != 1 {
		t.Fatal("release revived unknown queue", err)
	}
	if err := f.store.ConfigureRecovery(t.Context(), status.RecoveryID, f.service.recoveryEnabled); err != nil {
		t.Fatal("normal restart reheld released recovery", err)
	}
	f.service.RecoveryEnabled = func() bool { return false }
	restarted, _ = NewPGStore(f.pool, "platform")
	f.service.Store = restarted
	newMail := singleMail(t, f, "fresh-after-reviewed-release")
	for range 4 {
		if err := f.service.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.transport.envelopes) != 3 {
		t.Fatal("reviewed and fresh mail were not delivered once")
	}
	seen := map[string]int{}
	for _, e := range f.transport.envelopes {
		seen[e.RecipientID]++
	}
	if seen[sent.Recipients[0].ID] != 1 || seen[retry.Recipients[0].ID] != 1 || seen[newMail.Recipients[0].ID] != 1 || seen[unknown.Recipients[0].ID] != 0 {
		t.Fatal("restore produced duplicate or blind resend")
	}
	got, _ := restarted.Message(t.Context(), unknown.ID)
	if got.Recipients[0].Status != "queued" || got.Recipients[0].Attempts != 0 {
		t.Fatal("quarantine consumed attempt budget")
	}
	// A subsequent restore needs another fresh ID. Old operations replay their
	// original result, but can never activate an obsolete recovery generation.
	f.service.RecoveryEnabled = func() bool { return true }
	newStatus := startRecovery(t, f)
	if newStatus.Revision != 3 {
		t.Fatal("new recovery did not advance revision")
	}
	if err := f.store.ConfigureRecovery(t.Context(), status.RecoveryID, f.service.recoveryEnabled); !errors.Is(err, ErrRecoveryRevision) {
		t.Fatal("obsolete recovery ID was reused", err)
	}
	if _, err := f.service.ReleaseRecovery(t.Context(), release); err != nil {
		t.Fatal("saved release did not replay", err)
	}
	if err := f.store.checkRecovery(t.Context()); !errors.Is(err, ErrRecoveryHeld) {
		t.Fatal("old release reopened newer restore", err)
	}
}

func TestPGRecoveryPreservesCustodyAndLateReports(t *testing.T) {
	f := recoveryFixture(t)
	m := singleMail(t, f, "active-at-recovery")
	r, err := f.store.Claim(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.BeginSubmission(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	status := startRecovery(t, f)
	if status.ActiveSubmissions != 1 {
		t.Fatal("hold forgot active SMTP custody")
	}
	if _, err := f.service.ReviewRecovery(t.Context(), r.ID, recoveryReview(status, "submitting", "retry")); !errors.Is(err, ErrConflict) {
		t.Fatal("recovery stole live custody", err)
	}
	if err := f.store.Settle(t.Context(), r, "uncertain", "lost_data_response", time.Now()); err != nil {
		t.Fatal("hold blocked original settlement", err)
	}
	if err := f.store.Report(t.Context(), Report{EventID: "late-after-hold", RecipientID: r.ID, Kind: "accepted"}); err != nil {
		t.Fatal("recovery blocked late authenticated observation", err)
	}
	if _, err := f.service.ReviewRecovery(t.Context(), r.ID, recoveryReview(status, "uncertain", "retry")); !errors.Is(err, ErrConflict) {
		t.Fatal("stale operator review overwrote new evidence", err)
	}
	got, _ := f.store.Message(t.Context(), m.ID)
	if got.Recipients[0].Status != "accepted" {
		t.Fatal("late evidence was lost")
	}
}

func TestPGRecoveryHoldAndQuarantineSurviveFlagRollbackAndExpiry(t *testing.T) {
	f := recoveryFixture(t)
	m := retentionMessage(t, f, "old-queued-at-restore", "queued")
	terminal := retentionMessage(t, f, "terminal-before-restore", "accepted")
	status := startRecovery(t, f)
	f.service.RecoveryEnabled = func() bool { return false }
	f.delivery.Store(true)
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Claim(t.Context()); !errors.Is(err, ErrNotFound) {
		t.Fatal("direct worker claimed held mail", err)
	}
	if err := f.store.expireTimed(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Submit(t.Context(), "new-when-management-off", request()); !errors.Is(err, ErrRecoveryHeld) {
		t.Fatal("flag rollback bypassed persisted admission hold", err)
	}
	if _, err := f.service.RetainPayloads(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	assertPayloadRetained(t, f, m.ID)
	assertPayloadRetained(t, f, terminal.ID)
	f.service.RecoveryEnabled = func() bool { return true }
	if _, err := f.service.ReleaseRecovery(t.Context(), recoveryRelease(status)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Claim(t.Context()); !errors.Is(err, ErrNotFound) {
		t.Fatal("unknown old queue became eligible", err)
	}
	got, _ := f.store.Message(t.Context(), m.ID)
	if got.Recipients[0].Status != "queued" {
		t.Fatal("expiry erased unreviewed recovery evidence")
	}
	if _, err := f.service.RetainPayloads(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	assertPayloadRetained(t, f, m.ID)
}

func TestPGRecoveryHandoffFenceWaitsForRestoreAndProtectsClaims(t *testing.T) {
	f := recoveryFixture(t)
	m := singleMail(t, f, "claim-before-restore-hold")
	r, err := f.store.Claim(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	status := startRecovery(t, f)
	if err := f.store.BeginSubmission(t.Context(), r); !errors.Is(err, ErrRecoveryHeld) {
		t.Fatal("old rendered claim crossed recovery boundary", err)
	}
	if _, err := f.service.ReleaseRecovery(t.Context(), recoveryRelease(status)); err != nil {
		t.Fatal(err)
	}
	if err := f.store.BeginSubmission(t.Context(), r); !errors.Is(err, ErrRecoveryHeld) {
		t.Fatal("global release unquarantined old claim", err)
	}
	if err := f.store.Settle(t.Context(), r, "queued", "recovery_held", time.Now()); err != nil {
		t.Fatal(err)
	}
	got, _ := f.store.Message(t.Context(), m.ID)
	if got.Recipients[0].Attempts != 0 {
		t.Fatal("hold charged claim attempt")
	}
}

func TestPGRecoveryStartWaitsForAdmissionOwner(t *testing.T) {
	f := recoveryFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackOperator(t.Context(), tx)
	if err := f.store.queries.LockNamespace(ctx, tx, "platform"); err != nil {
		t.Fatal(err)
	}
	admitted := make(chan error, 1)
	go func() {
		_, err := f.service.Submit(ctx, "admission-before-restore", request())
		admitted <- err
	}()
	admissionPID := blockedEncryptionPID(t, f, tx.Conn().PgConn().PID())
	held := make(chan error, 1)
	go func() { held <- f.store.ConfigureRecovery(ctx, uuid.NewString(), f.service.recoveryEnabled) }()
	blockedEncryptionPID(t, f, uint32(admissionPID))
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, done := range []chan error{admitted, held} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("recovery owner fences did not drain")
		}
	}
	status, err := f.service.RecoveryStatus(t.Context())
	if err != nil || !status.Held || status.Quarantined != 2 {
		t.Fatal("recovery skipped admission committed before hold", err)
	}
	if _, err := f.service.Submit(ctx, "after-owner-fence", request()); !errors.Is(err, ErrRecoveryHeld) {
		t.Fatal("admission crossed committed recovery hold", err)
	}
}

func TestPGRecoveryRevisionKeysDisableAndNamespaceChecks(t *testing.T) {
	f := recoveryFixture(t)
	m := singleMail(t, f, "review-cas")
	status := startRecovery(t, f)
	stale := recoveryRelease(status)
	wrong := int64(0)
	stale.ExpectedRevision = &wrong
	if _, err := f.service.ReleaseRecovery(t.Context(), stale); !errors.Is(err, ErrInvalid) {
		t.Fatal("invalid release revision accepted", err)
	}
	stale = recoveryRelease(status)
	wrong = 99
	stale.ExpectedRevision = &wrong
	if _, err := f.service.ReleaseRecovery(t.Context(), stale); !errors.Is(err, ErrRecoveryRevision) {
		t.Fatal("stale release accepted", err)
	}
	input := recoveryReview(status, "queued", "retry")
	calls := 0
	if _, err := f.store.reviewRecovery(t.Context(), m.Recipients[0].ID, input, func() bool { calls++; return calls == 1 }); !errors.Is(err, ErrDisabled) {
		t.Fatal("late disable committed review", err)
	}
	preview, _ := f.service.PreviewRecovery(t.Context(), OperatorList{Limit: 25})
	if len(preview.Items) != 1 {
		t.Fatal("rollback cleared quarantine")
	}
	// Even with confirmations, all restored encryption readers are required.
	legacy := f.service.Codec.(*KeyringCodec).legacyID
	missing := keyringForTest(t, encryptionIdentity(t), encryptionIdentity(t))
	missing.legacyID = legacy
	f.service.Codec = missing
	if _, err := f.service.ReleaseRecovery(t.Context(), recoveryRelease(status)); !errors.Is(err, ErrEncryptionKeyUnavailable) {
		t.Fatal("release accepted missing read key", err)
	}
	other, _ := NewPGStore(f.pool, "other_namespace")
	if err := other.checkRecovery(t.Context()); err != nil {
		t.Fatal("hold leaked namespace", err)
	}
	otherStatus, err := other.recoveryStatus(t.Context())
	if err != nil || otherStatus.Held || otherStatus.Quarantined != 0 || otherStatus.Revision != 0 {
		t.Fatal("namespace disclosed recovery inventory", err)
	}
}

func TestPGRecoveryPrivateHTTPReviewAndRelease(t *testing.T) {
	f := recoveryFixture(t)
	m := singleMail(t, f, "http-review")
	status := startRecovery(t, f)
	h, err := Handler(f.service, testToken)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{RecoveryPath, RecoveryPath + "/preview?limit=1"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set("Authorization", "Bearer "+operatorToken)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 || !strings.Contains(w.Body.String(), status.RecoveryID) {
			t.Fatal("private recovery status failed", w.Code)
		}
	}
	input := recoveryReview(status, "queued", "submitted")
	raw, _ := json.Marshal(input)
	r := httptest.NewRequest(http.MethodPost, RecoveryPath+"/recipients/"+m.Recipients[0].ID, bytes.NewReader(raw))
	r.Header.Set("Authorization", "Bearer "+operatorToken)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal("HTTP review failed", w.Code, w.Body.String())
	}
	raw, _ = json.Marshal(recoveryRelease(status))
	r = httptest.NewRequest(http.MethodPost, RecoveryPath+"/release", bytes.NewReader(raw))
	r.Header.Set("Authorization", "Bearer "+operatorToken)
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal("HTTP release failed", w.Code, w.Body.String())
	}
}

func TestPGRecoveryMigrationIsIdempotentAndProtectsUsedState(t *testing.T) {
	f := recoveryFixture(t)
	raw, err := migrations.FS.ReadFile("20261016181202093_internal_mail_recovery.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(raw), "-- +goose StatementBegin\n")
	up := strings.Split(parts[1], "-- +goose StatementEnd")[0]
	down := strings.Split(parts[2], "-- +goose StatementEnd")[0]
	if _, err := f.pool.Exec(t.Context(), down); err != nil {
		t.Fatal(err)
	}
	if f.store.Ready(t.Context()) == nil {
		t.Fatal("old schema passed readiness")
	}
	for range 2 {
		if _, err := f.pool.Exec(t.Context(), up); err != nil {
			t.Fatal(err)
		}
	}
	status := startRecovery(t, f)
	if _, err := f.service.ReleaseRecovery(t.Context(), recoveryRelease(status)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), down); err == nil {
		t.Fatal("rollback discarded used recovery state")
	}
}

func TestPGRecoveryBlocksProducerAuthenticationAndFeedUntilReviewedRelease(t *testing.T) {
	f := eventFeedFixture(t)
	legacy := f.service.Codec.(AgeCodec).Identity
	f.service.Codec = keyringForTest(t, legacy, legacy)
	f.service.RecoveryEnabled = func() bool { return true }
	feedTarget(t, f, "auth")
	publishFeed(t, f)
	batch := pullFeed(t, f, "auth", "effects", 25)
	before := feedStatus(t, f, "auth", "effects")
	status := startRecovery(t, f)
	// Rollback of management must not expose rewound producer credentials or
	// allow a stale feed receipt to move its durable checkpoint.
	f.service.RecoveryEnabled = func() bool { return false }
	if err := f.store.checkProducerAuthentication(t.Context(), producerAuthentication{ProducerID: "auth"}); !errors.Is(err, ErrRecoveryHeld) {
		t.Fatal("held producer authenticated", err)
	}
	if _, err := f.service.PullEventFeed(t.Context(), "auth", "effects", 25); !errors.Is(err, ErrRecoveryHeld) {
		t.Fatal("held feed pulled", err)
	}
	if _, err := f.service.AckEventFeed(t.Context(), "auth", "effects", EventFeedAckInput{Receipt: batch.Receipt, Generation: batch.Generation}); !errors.Is(err, ErrRecoveryHeld) {
		t.Fatal("held feed acknowledged", err)
	}
	after := feedStatus(t, f, "auth", "effects")
	if before.Cursor != after.Cursor || before.PendingReceipt != after.PendingReceipt || before.Generation != after.Generation {
		t.Fatal("held receipt changed consumer state")
	}
	f.service.RecoveryEnabled = func() bool { return true }
	if _, err := f.service.ReleaseRecovery(t.Context(), recoveryRelease(status)); err != nil {
		t.Fatal(err)
	}
	ackFeed(t, f, batch)
}

func TestPGRecoveryReviewCannotBypassOtherControlsOrChangeKnownRelayCustody(t *testing.T) {
	f := recoveryFixture(t)
	f.service.SuppressionControlsEnabled = func() bool { return true }
	m := singleMail(t, f, "suppressed-restored-recipient")
	known := singleMail(t, f, "known-relay-owned")
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_recipients SET status='submitted' WHERE id=$1", known.Recipients[0].ID); err != nil {
		t.Fatal(err)
	}
	changeSuppression(t, f, "add", suppressionInput("one@example.net", 0, "add"))
	status := startRecovery(t, f)
	if _, err := f.service.ReviewRecovery(t.Context(), known.Recipients[0].ID, recoveryReview(status, "submitted", "retry")); !errors.Is(err, ErrConflict) {
		t.Fatal("recovery retried known relay ownership", err)
	}
	if _, err := f.service.ReviewRecovery(t.Context(), m.Recipients[0].ID, recoveryReview(status, "queued", "retry")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.ReleaseRecovery(t.Context(), recoveryRelease(status)); err != nil {
		t.Fatal(err)
	}
	f.delivery.Store(true)
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	got, err := f.store.Message(t.Context(), m.ID)
	if err != nil || got.Recipients[0].Status != "suppressed" || len(f.transport.envelopes) != 0 {
		t.Fatal("recovery bypassed independent suppression", err)
	}
}
