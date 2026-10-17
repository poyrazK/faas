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
	"sync/atomic"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/migrations"
	q "github.com/onebox-faas/faas/pkg/internalmail/sqlc"
)

func retentionFixture(t *testing.T) *pgFixture {
	t.Helper()
	f := fixture(t)
	f.service.RetentionEnabled = func() bool { return true }
	f.service.RetentionPolicy = DefaultRetentionPolicy()
	f.service.RetentionPolicy.DryRun = false
	return f
}

func retentionMessage(t *testing.T, f *pgFixture, key, state string) MessageResponse {
	t.Helper()
	input := request()
	input.To = []string{"one@example.net"}
	m := submit(t, f, key, input)
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_messages SET created_at=now()-interval '8 days' WHERE id=$1", m.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE internal_mail_recipients SET status=$2,attempts=1,updated_at=now()-interval '8 days',
lease_id=CASE WHEN $2 IN ('claimed','submitting') THEN $3::uuid ELSE NULL END,
lease_until=CASE WHEN $2 IN ('claimed','submitting') THEN now()+interval '1 hour' ELSE NULL END WHERE message_id=$1`, m.ID, state, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	return m
}

func assertPayloadRetained(t *testing.T, f *pgFixture, id string) {
	t.Helper()
	m, err := f.store.Message(t.Context(), id)
	if err != nil || len(m.SealedPayload) == 0 || m.PayloadPrunedAt != nil {
		t.Fatal("protected payload was removed", err)
	}
}

func TestPGRetentionPreviewPruneAndIdempotencyAfterRestart(t *testing.T) {
	f := retentionFixture(t)
	m := retentionMessage(t, f, "retention-replay", "accepted")
	codec := f.service.Codec
	f.service.Codec = nil // A dry run must never decrypt or fingerprint bodies.
	preview, err := f.service.RetainPayloads(t.Context(), true)
	if err != nil || !preview.DryRun || preview.EligibleMessages != 1 || preview.EligibleBytes != int64(len(m.SealedPayload)) || preview.PrunedMessages != 0 {
		t.Fatal("invalid read-only preview", preview, err)
	}
	assertPayloadRetained(t, f, m.ID)
	f.service.Codec = codec
	result, err := f.service.RetainPayloads(t.Context(), false)
	if err != nil || result.PrunedMessages != 1 || result.PrunedBytes != int64(len(m.SealedPayload)) {
		t.Fatal("payload not pruned", result, err)
	}
	restarted, err := NewPGStore(f.pool, "platform")
	if err != nil {
		t.Fatal(err)
	}
	f.service.Store = restarted
	after, err := restarted.Message(t.Context(), m.ID)
	if err != nil || len(after.SealedPayload) != 0 || after.PayloadPrunedAt == nil || !bytes.Equal(m.PayloadFingerprint, after.PayloadFingerprint) || after.Recipients[0].Status != "accepted" {
		t.Fatal("pruning removed correlation or verifier metadata", err)
	}
	input := request()
	input.To = []string{"one@example.net"}
	replay, err := f.service.Submit(t.Context(), "retention-replay", input)
	if err != nil || replay.ID != m.ID || len(replay.Recipients) != 1 {
		t.Fatal("pruned message lost idempotency", err)
	}
	input.Text = "different content"
	if _, err := f.service.Submit(t.Context(), "retention-replay", input); !errors.Is(err, ErrConflict) {
		t.Fatal("pruned message accepted contradictory replay", err)
	}
	result, err = f.service.RetainPayloads(t.Context(), false)
	if err != nil || result.PrunedMessages != 0 || result.EligibleMessages != 0 {
		t.Fatal("cleanup replay did more work", result, err)
	}
	raw, err := json.Marshal(after)
	if err != nil || bytes.Contains(raw, []byte("123456")) || bytes.Contains(raw, []byte("fingerprint")) || bytes.Contains(raw, []byte("sealed_payload")) {
		t.Fatal("retention metadata API exposed private content", err)
	}
	stats, err := restarted.RetentionStatistics(t.Context(), f.service.RetentionPolicy)
	if err != nil || stats.PrunedMessages != 1 || stats.RetainedBytes != 0 || stats.EligibleMessages != 0 || len(f.transport.envelopes) != 0 {
		t.Fatal("durable retention statistics or no-resend guarantee failed", stats, err)
	}
}

func TestPGRetentionProtectsEveryUnsettledStateAndFreshTerminalEvidence(t *testing.T) {
	f := retentionFixture(t)
	var protected []string
	for _, state := range []string{"queued", "claimed", "submitting", "submitted", "deferred", "uncertain", "accepted", "bounced", "complained", "suppressed", "failed"} {
		m := retentionMessage(t, f, "state-"+state, state)
		if state == "queued" || state == "claimed" || state == "submitting" || state == "submitted" || state == "deferred" || state == "uncertain" {
			protected = append(protected, m.ID)
		}
	}
	fresh := retentionMessage(t, f, "fresh-observation", "accepted")
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_recipients SET updated_at=now() WHERE message_id=$1", fresh.ID); err != nil {
		t.Fatal(err)
	}
	protected = append(protected, fresh.ID)
	newMessage := retentionMessage(t, f, "fresh-message", "accepted")
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_messages SET created_at=now() WHERE id=$1", newMessage.ID); err != nil {
		t.Fatal(err)
	}
	protected = append(protected, newMessage.ID)
	mixed := retentionMessage(t, f, "mixed-message", "accepted")
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO internal_mail_recipients(id,namespace,message_id,address,status,updated_at)
VALUES($1,'platform',$2,'two@example.net','uncertain',now()-interval '8 days')`, uuid.NewString(), mixed.ID); err != nil {
		t.Fatal(err)
	}
	protected = append(protected, mixed.ID)
	orphan := retentionMessage(t, f, "missing-recipient-evidence", "accepted")
	if _, err := f.pool.Exec(t.Context(), "DELETE FROM internal_mail_recipients WHERE message_id=$1", orphan.ID); err != nil {
		t.Fatal(err)
	}
	protected = append(protected, orphan.ID)
	preview, err := f.service.RetainPayloads(t.Context(), true)
	if err != nil || preview.EligibleMessages != 5 {
		t.Fatal("preview did not isolate old terminal work", preview, err)
	}
	result, err := f.service.RetainPayloads(t.Context(), false)
	if err != nil || result.PrunedMessages != 5 {
		t.Fatal("cleanup disagreed with preview", result, err)
	}
	for _, id := range protected {
		assertPayloadRetained(t, f, id)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_recipients SET lease_until=now()-interval '1 hour' WHERE status IN ('claimed','submitting')"); err != nil {
		t.Fatal(err)
	}
	if result, err := f.service.RetainPayloads(t.Context(), false); err != nil || result.PrunedMessages != 0 {
		t.Fatal("expired in-flight leases lost payloads", result, err)
	}
}

func TestPGRetentionBackfillsLegacyVerifierAndPreservesLateFeedback(t *testing.T) {
	f := retentionFixture(t)
	signer := enableFeedback(t, f)
	m := retentionMessage(t, f, "legacy-feedback", "accepted")
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_messages SET payload_fingerprint=NULL WHERE id=$1", m.ID); err != nil {
		t.Fatal(err)
	}
	if result, err := f.service.RetainPayloads(t.Context(), false); err != nil || result.PrunedMessages != 1 {
		t.Fatal("legacy payload was not verified and pruned", result, err)
	}
	input := request()
	input.To = []string{"one@example.net"}
	if replay, err := f.service.Submit(t.Context(), "legacy-feedback", input); err != nil || replay.ID != m.ID {
		t.Fatal("legacy replay broke after backfill", err)
	}
	for _, raw := range [][]byte{
		sampleFeedback(m.Recipients[0].ID, "example.test", "one@example.net", "dsn", "5.1.1"),
		sampleFeedback(m.Recipients[0].ID, "example.test", "one@example.net", "arf", ""),
	} {
		signed := signer.sign(t, raw, "feedback.provider.test", nil)
		for range 2 {
			if err := f.service.IngestFeedback(t.Context(), "provider", signed); err != nil {
				t.Fatal("cleanup destroyed late feedback correlation or replay", err)
			}
		}
	}
	after, err := f.store.Message(t.Context(), m.ID)
	if err != nil || after.Recipients[0].Status != "complained" || len(after.SealedPayload) != 0 || after.PayloadPrunedAt == nil || feedbackEventCount(t, f, m.ID) != 2 {
		t.Fatal("late feedback lost delivery history", err)
	}
	if suppressed, err := f.store.Suppressed(t.Context(), "one@example.net"); err != nil || !suppressed {
		t.Fatal("cleanup lost suppression evidence", err)
	}
	next := submit(t, f, "suppressed-after-pruning", input)
	f.delivery.Store(true)
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	after, err = f.store.Message(t.Context(), next.ID)
	if err != nil || after.Recipients[0].Status != "suppressed" || len(f.transport.envelopes) != 0 {
		t.Fatal("cleanup permitted a suppressed destination to be sent again", err)
	}
}

func TestPGRetentionIsBoundedAndNamespaceScopedUnderConcurrency(t *testing.T) {
	f := retentionFixture(t)
	f.service.RetentionPolicy.BatchSize = 1
	for _, key := range []string{"first", "second"} {
		retentionMessage(t, f, key, "accepted")
	}
	other, err := NewPGStore(f.pool, "other")
	if err != nil {
		t.Fatal(err)
	}
	copyService := *f.service
	copyService.Store = other
	if result, err := copyService.RetainPayloads(t.Context(), false); err != nil || result.EligibleMessages != 0 {
		t.Fatal("cleanup crossed namespaces", result, err)
	}
	var pruned atomic.Int32
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() {
			result, err := f.service.RetainPayloads(t.Context(), false)
			if err != nil || result.EligibleMessages > 1 || result.PrunedMessages > 1 {
				t.Error("cleanup exceeded its batch", result, err)
			}
			pruned.Add(int32(result.PrunedMessages))
		})
	}
	workers.Wait()
	if pruned.Load() != 2 {
		t.Fatal("concurrent cleanup duplicated or lost work", pruned.Load())
	}
}

func TestPGRetentionSkipsBusyRecipientsAndRechecksFreshReports(t *testing.T) {
	f := retentionFixture(t)
	m := retentionMessage(t, f, "busy-recipient", "accepted")
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	if _, err := tx.Exec(t.Context(), "SELECT id FROM internal_mail_recipients WHERE message_id=$1 FOR UPDATE", m.ID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	result, err := f.service.RetainPayloads(ctx, false)
	if err != nil || result.PrunedMessages != 0 || result.SkippedMessages != 1 {
		t.Fatal("cleanup blocked or removed a busy delivery", result, err)
	}
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	tx, err = f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	if _, err := tx.Exec(t.Context(), "SELECT id FROM internal_mail_messages WHERE id=$1 FOR UPDATE", m.ID); err != nil {
		t.Fatal(err)
	}
	if result, err := f.service.RetainPayloads(ctx, false); err != nil || result.PrunedMessages != 0 || result.EligibleMessages != 0 {
		t.Fatal("cleanup blocked or removed a busy message", result, err)
	}
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	// A report can refresh terminal evidence between candidate selection and
	// recipient locks. The final update must evaluate eligibility again.
	tx, err = f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	rows, err := f.store.queries.RetentionCandidates(t.Context(), tx, q.RetentionCandidatesParams{Namespace: "platform", RetentionDays: 7, BatchSize: 1})
	if err != nil || len(rows) != 1 {
		t.Fatal("candidate not selected", err)
	}
	if err := f.store.Report(t.Context(), Report{EventID: "fresh-after-selection", RecipientID: m.Recipients[0].ID, Kind: "accepted", Code: "smtp_250"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.queries.LockRetentionRecipients(t.Context(), tx, q.LockRetentionRecipientsParams{Namespace: "platform", MessageID: pgID(m.ID)}); err != nil {
		t.Fatal(err)
	}
	n, err := f.store.queries.PrunePayload(t.Context(), tx, q.PrunePayloadParams{Namespace: "platform", ID: pgID(m.ID), RetentionDays: 7, Fingerprint: m.PayloadFingerprint})
	if err != nil || n != 0 {
		t.Fatal("cleanup ignored newly committed evidence", n, err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertPayloadRetained(t, f, m.ID)
}

func TestPGRetentionRollbackOnCorruptionAndGateRevocation(t *testing.T) {
	f := retentionFixture(t)
	first := retentionMessage(t, f, "first-before-error", "accepted")
	second := retentionMessage(t, f, "second-error", "accepted")
	// Fix order so the first row is updated before the second fails.
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_messages SET created_at=now()-interval '9 days' WHERE id=$1", first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_messages SET sealed_payload=$2 WHERE id=$1", second.ID, []byte("corrupt encrypted payload")); err != nil {
		t.Fatal(err)
	}
	if result, err := f.service.RetainPayloads(t.Context(), false); err == nil || result.PrunedMessages != 0 {
		t.Fatal("corrupt payload did not roll back the batch", result, err)
	}
	assertPayloadRetained(t, f, first.ID)
	assertPayloadRetained(t, f, second.ID)
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_messages SET sealed_payload=$2 WHERE id=$1", second.ID, second.SealedPayload); err != nil {
		t.Fatal(err)
	}
	var enabled atomic.Bool
	enabled.Store(true)
	f.service.RetentionEnabled = enabled.Load
	prepared := 0
	result, err := f.store.applyRetention(t.Context(), f.service.RetentionPolicy, func(m Message) ([]byte, error) {
		fingerprint, err := f.service.retentionFingerprint(m)
		prepared++
		if prepared == 2 {
			enabled.Store(false)
		}
		return fingerprint, err
	}, enabled.Load)
	if !errors.Is(err, ErrDisabled) || result.PrunedMessages != 0 || prepared != 2 {
		t.Fatal("revoked gate committed cleanup", result, err)
	}
	assertPayloadRetained(t, f, first.ID)
	assertPayloadRetained(t, f, second.ID)
	enabled.Store(true)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	prepared = 0
	result, err = f.store.applyRetention(ctx, f.service.RetentionPolicy, func(m Message) ([]byte, error) {
		fingerprint, err := f.service.retentionFingerprint(m)
		prepared++
		if prepared == 2 {
			cancel()
		}
		return fingerprint, err
	}, enabled.Load)
	if err == nil || result.PrunedMessages != 0 {
		t.Fatal("cancelled pass committed partial cleanup", result, err)
	}
	assertPayloadRetained(t, f, first.ID)
	assertPayloadRetained(t, f, second.ID)
	wrongIdentity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	f.service.Codec = AgeCodec{Identity: wrongIdentity, Namespace: "platform"}
	if _, err := f.service.RetainPayloads(t.Context(), false); err == nil {
		t.Fatal("wrong identity destroyed recoverable encrypted content")
	}
	assertPayloadRetained(t, f, first.ID)
}

func TestPGRetentionHTTPDefaultsToPreviewAndRejectsPolicyOverride(t *testing.T) {
	f := retentionFixture(t)
	m := retentionMessage(t, f, "http-preview", "accepted")
	handler, err := Handler(f.service, testToken)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		input string
		want  int
	}{{`{}`, 200}, {`{"dry_run":null}`, 200}, {`{"dry_run":true}`, 200}, {`{"dry_run":false,"days":0}`, 400}} {
		r := httptest.NewRequest(http.MethodPost, RetentionPath, strings.NewReader(tc.input))
		r.Header.Set("Authorization", "Bearer "+testToken)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.want || strings.Contains(w.Body.String(), m.ID) || strings.Contains(w.Body.String(), "one@example.net") {
			t.Fatal("unsafe retention preview response", w.Code)
		}
		assertPayloadRetained(t, f, m.ID)
	}
}

func TestPGRetentionMigrationReplaysAndRejectsUnsafeRollback(t *testing.T) {
	f := retentionFixture(t)
	raw, err := migrations.FS.ReadFile("20261015151448279_internal_mail_payload_retention.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(raw), "-- +goose StatementBegin\n")
	up := strings.Split(parts[1], "-- +goose StatementEnd")[0]
	down := strings.Split(parts[2], "-- +goose StatementEnd")[0]
	if _, err := f.pool.Exec(t.Context(), down); err != nil {
		t.Fatal("empty migration rollback failed", err)
	}
	if _, err := f.pool.Exec(t.Context(), down); err != nil {
		t.Fatal("empty rollback replay failed", err)
	}
	if err := f.store.Ready(t.Context()); err == nil {
		t.Fatal("old schema reported ready")
	}
	for range 2 {
		if _, err := f.pool.Exec(t.Context(), up); err != nil {
			t.Fatal("migration replay failed", err)
		}
	}
	m := retentionMessage(t, f, "rollback-protected", "accepted")
	if _, err := f.service.RetainPayloads(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), down); err == nil {
		t.Fatal("schema rollback discarded pruned-message verifiers")
	}
	after, err := f.store.Message(t.Context(), m.ID)
	if err != nil || after.PayloadPrunedAt == nil || len(after.PayloadFingerprint) != 32 {
		t.Fatal("failed rollback changed retention metadata", err)
	}
}
