package internalmail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/onebox-faas/faas/migrations"
)

// DKIM records the current signing time. Compare the unchanged message and
// independently verify both signatures instead of requiring an identical t=.
func encryptionMessageContent(t *testing.T, raw []byte) any {
	t.Helper()
	m, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	delete(m.Header, "Dkim-Signature")
	body, err := io.ReadAll(m.Body)
	if err != nil {
		t.Fatal(err)
	}
	return struct {
		Header mail.Header
		Body   string
	}{m.Header, string(body)}
}

func encryptionFixture(t *testing.T) (*pgFixture, *age.X25519Identity, *age.X25519Identity) {
	t.Helper()
	f := domainFixture(t)
	legacy := f.service.Codec.(AgeCodec).Identity
	next := encryptionIdentity(t)
	codec := keyringForTest(t, legacy, legacy, next)
	if err := f.store.ConfigureEncryption(t.Context(), codec); err != nil {
		t.Fatal(err)
	}
	f.service.Codec = codec
	f.service.EncryptionRotationEnabled = func() bool { return true }
	return f, legacy, next
}

func encryptionInput(id string, revision int64, size int) EncryptionRequest {
	return EncryptionRequest{OperatorAuditInput: auditInput(), KeyID: id, ExpectedRevision: &revision, BatchSize: size}
}

func changeEncryption(t *testing.T, f *pgFixture, action string, input EncryptionRequest) OperationRecord {
	t.Helper()
	record, err := f.service.ChangeEncryption(t.Context(), action, input)
	if err != nil {
		t.Fatal("encryption operation failed", action, err)
	}
	return record
}

func rewrapAll(t *testing.T, f *pgFixture, id string, revision int64) int {
	t.Helper()
	total := 0
	for range 30 {
		record := changeEncryption(t, f, "rewrap", encryptionInput(id, revision, 1))
		var result EncryptionBatch
		if err := json.Unmarshal(record.Result, &result); err != nil || result.Rewrapped > 1 || result.KeyID != id {
			t.Fatal("migration exceeded its bound or changed target", err)
		}
		total += result.Rewrapped
		if !result.BatchFull {
			return total
		}
	}
	t.Fatal("bounded migration made no progress")
	return total
}

func TestPGEncryptionRotationMigratesLegacyAndSelectedDKIMWithoutChangingMail(t *testing.T) {
	f, legacy, next := encryptionFixture(t)
	m := singleMail(t, f, "legacy-key-rotation")
	key := verifiedKey(t, f)
	if _, err := f.service.ActivateDKIMKey(t.Context(), f.domain.ID, key.ID, auditInput()); err != nil {
		t.Fatal(err)
	}
	domainBefore, _ := f.store.Domain(t.Context(), f.domain.ID)
	renderBefore, err := f.service.render(m.Message, m.Recipients[0], domainBefore)
	if err != nil {
		t.Fatal(err)
	}
	eventsBefore, _ := f.store.Events(t.Context(), m.ID)
	// Old rows without any HMAC are migrated from their authenticated payload.
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_messages SET payload_fingerprint=NULL WHERE id=$1", m.ID); err != nil {
		t.Fatal(err)
	}
	nextID, oldID := EncryptionKeyID(next), EncryptionKeyID(legacy)
	activation := encryptionInput(nextID, 0, 0)
	saved := changeEncryption(t, f, "activate", activation)
	status, err := f.service.EncryptionStatus(t.Context())
	if err != nil || status.Revision != 1 || !status.Versioned || status.ActiveKeyID != nextID || status.LegacyKeyID != oldID {
		t.Fatal("activation lost durable selection", err)
	}
	newMail := singleMail(t, f, "new-key-rotation")
	if id, err := envelopeKeyID(newMail.SealedPayload); err != nil || id != nextID {
		t.Fatal("new mail did not use the selected identity", err)
	}
	preview, err := f.service.PreviewEncryption(t.Context(), 1)
	if err != nil || !preview.DryRun || !preview.BatchFull || preview.Rewrapped != 0 || preview.DomainKeys+preview.DKIMKeys+preview.Payloads != 1 {
		t.Fatal("invalid bounded dry run", err)
	}
	domainUnchanged, _ := f.store.Domain(t.Context(), f.domain.ID)
	if !bytes.Equal(domainBefore.SealedKey, domainUnchanged.SealedKey) {
		t.Fatal("preview changed ciphertext")
	}
	if _, err := f.service.ChangeEncryption(t.Context(), "retire", encryptionInput(oldID, 1, 0)); !errors.Is(err, ErrEncryptionKeyRequired) {
		t.Fatal("retired key with retained ciphertext", err)
	}
	if total := rewrapAll(t, f, nextID, 1); total != 4 {
		t.Fatal("did not migrate the legacy payload, selected signer and key history", total)
	}
	after, _ := f.store.Message(t.Context(), m.ID)
	domainAfter, _ := f.store.Domain(t.Context(), f.domain.ID)
	renderAfter, err := f.service.render(after.Message, after.Recipients[0], domainAfter)
	if err != nil || !reflect.DeepEqual(encryptionMessageContent(t, renderBefore), encryptionMessageContent(t, renderAfter)) {
		t.Fatal("re-encryption changed the signed message or DKIM selection", err)
	}
	assertSignedWith(t, f, renderBefore, key.Selector)
	assertSignedWith(t, f, renderAfter, key.Selector)
	if !reflect.DeepEqual(m.Recipients, after.Recipients) || !m.CreatedAt.Equal(after.CreatedAt) || !sameTiming(m.MessageTiming, after.MessageTiming) || after.ProducerID != m.ProducerID || after.IdempotencyKey != m.IdempotencyKey {
		t.Fatal("migration changed delivery state or ownership")
	}
	eventsAfter, _ := f.store.Events(t.Context(), m.ID)
	if !reflect.DeepEqual(eventsBefore, eventsAfter) || domainAfter.DNSRevision != domainBefore.DNSRevision || len(f.transport.envelopes) != 0 {
		t.Fatal("migration changed DNS, delivery evidence or SMTP custody")
	}
	changeEncryption(t, f, "retire", encryptionInput(oldID, 1, 0))
	codec := keyringForTest(t, legacy, next)
	if err := f.store.ConfigureEncryption(t.Context(), codec); err != nil {
		t.Fatal("zero-reference retired key was still required at restart", err)
	}
	f.service.Codec = codec
	input := request()
	input.To = []string{"one@example.net"}
	replay, err := f.service.Submit(t.Context(), "legacy-key-rotation", input)
	if err != nil || replay.ID != m.ID {
		t.Fatal("migration or key removal broke replay", err)
	}
	input.Text = "contradictory content"
	if _, err := f.service.Submit(t.Context(), "legacy-key-rotation", input); !errors.Is(err, ErrConflict) {
		t.Fatal("migrated replay accepted different content", err)
	}
	if _, err := f.service.ChangeEncryption(t.Context(), "activate", encryptionInput(oldID, 2, 0)); err == nil {
		t.Fatal("retired identity was reactivated")
	}
	replayed := changeEncryption(t, f, "activate", activation)
	if !replayed.replayed || replayed.ID != saved.ID || !bytes.Equal(replayed.Result, saved.Result) {
		t.Fatal("saved operation replay depended on the removed key")
	}
	status, _ = f.service.EncryptionStatus(t.Context())
	if status.Revision != 2 || status.ActiveKeyID != nextID {
		t.Fatal("saved operation overwrote newer selection")
	}
	restored := keyringForTest(t, legacy, legacy, next)
	if err := f.store.ConfigureEncryption(t.Context(), restored); err != nil {
		t.Fatal(err)
	}
	f.service.Codec = restored
	if _, err := f.service.ChangeEncryption(t.Context(), "activate", encryptionInput(oldID, 2, 0)); !errors.Is(err, ErrConflict) {
		t.Fatal("restoring a private key revived a permanently retired identity", err)
	}
	activation.Evidence = "ticket:changed"
	if _, err := f.service.ChangeEncryption(t.Context(), "activate", activation); !errors.Is(err, ErrConflict) {
		t.Fatal("changed audit request reused a saved request ID", err)
	}
	var audit string
	if err := f.pool.QueryRow(t.Context(), "SELECT string_agg(result::text,' ') FROM internal_mail_operations WHERE action LIKE 'encryption_%'").Scan(&audit); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{legacy.String(), next.String(), "notify@", "one@", "123456", "sealed_payload", "fingerprint"} {
		if strings.Contains(audit, secret) {
			t.Fatal("key operation audit exposed private material")
		}
	}
}

func TestPGEncryptionPrunedLegacyAndVersionedFingerprintsBlockRetirement(t *testing.T) {
	f, legacy, next := encryptionFixture(t)
	old := retentionMessage(t, f, "legacy-pruned-key", "accepted")
	if result, err := f.service.RetainPayloads(t.Context(), false); err != nil || result.PrunedMessages != 1 {
		t.Fatal("legacy cleanup failed", err)
	}
	nextID := EncryptionKeyID(next)
	changeEncryption(t, f, "activate", encryptionInput(nextID, 0, 0))
	versioned := retentionMessage(t, f, "versioned-pruned-key", "accepted")
	if result, err := f.service.RetainPayloads(t.Context(), false); err != nil || result.PrunedMessages != 1 {
		t.Fatal("versioned cleanup failed", err)
	}
	third := encryptionIdentity(t)
	codec := keyringForTest(t, legacy, legacy, next, third)
	if err := f.store.ConfigureEncryption(t.Context(), codec); err != nil {
		t.Fatal(err)
	}
	f.service.Codec = codec
	changeEncryption(t, f, "activate", encryptionInput(EncryptionKeyID(third), 1, 0))
	rewrapAll(t, f, EncryptionKeyID(third), 2)
	status, err := f.service.EncryptionStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{EncryptionKeyID(legacy), nextID} {
		found := false
		for _, key := range status.Keys {
			if key.KeyID == id {
				found = key.PrunedFingerprints == 1 && key.Payloads+key.DomainKeys+key.DKIMKeys == 0
			}
		}
		if !found {
			t.Fatal("pruned HMAC dependency absent from inspection")
		}
		if _, err := f.service.ChangeEncryption(t.Context(), "retire", encryptionInput(id, 2, 0)); !errors.Is(err, ErrEncryptionKeyRequired) {
			t.Fatal("retired the only surviving replay verifier", err)
		}
	}
	missing := keyringForTest(t, legacy, third)
	if err := f.store.ConfigureEncryption(t.Context(), missing); !errors.Is(err, ErrEncryptionKeyUnavailable) {
		t.Fatal("startup accepted missing pruned-message verifier keys", err)
	}
	// Runtime rollback keeps reads and owner fences while removing management.
	f.service.EncryptionRotationEnabled = func() bool { return false }
	for _, tc := range []struct {
		key     string
		message MessageResponse
	}{{"legacy-pruned-key", old}, {"versioned-pruned-key", versioned}} {
		input := request()
		input.To = []string{"one@example.net"}
		replay, err := f.service.Submit(t.Context(), tc.key, input)
		if err != nil || replay.ID != tc.message.ID || replay.PayloadPrunedAt == nil || len(replay.SealedPayload) != 0 {
			t.Fatal("pruned replay was lost during key or management rotation", err)
		}
		input.Subject = "changed subject"
		if _, err := f.service.Submit(t.Context(), tc.key, input); !errors.Is(err, ErrConflict) {
			t.Fatal("pruned replay ignored changed content", err)
		}
	}
	if _, err := f.service.EncryptionStatus(t.Context()); !errors.Is(err, ErrDisabled) {
		t.Fatal("disabled management inspected key metadata", err)
	}
}

func TestPGEncryptionBatchFailureAndGateRollbackAreAtomic(t *testing.T) {
	for _, failure := range []string{"fingerprint", "disabled"} {
		t.Run(failure, func(t *testing.T) {
			f, _, next := encryptionFixture(t)
			m := singleMail(t, f, "atomic-encryption")
			domain, _ := f.store.Domain(t.Context(), f.domain.ID)
			changeEncryption(t, f, "activate", encryptionInput(EncryptionKeyID(next), 0, 0))
			input := encryptionInput(EncryptionKeyID(next), 1, 25)
			var err error
			if failure == "fingerprint" {
				if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_messages SET payload_fingerprint=$2 WHERE id=$1", m.ID, make([]byte, 32)); err != nil {
					t.Fatal(err)
				}
				_, err = f.service.ChangeEncryption(t.Context(), "rewrap", input)
			} else {
				calls := 0
				_, err = f.store.changeEncryption(t.Context(), "rewrap", input, f.service.Codec.(*KeyringCodec), func() bool { calls++; return calls <= 2 })
			}
			if err == nil {
				t.Fatal("failed batch committed")
			}
			afterDomain, _ := f.store.Domain(t.Context(), f.domain.ID)
			afterMail, _ := f.store.Message(t.Context(), m.ID)
			if !bytes.Equal(domain.SealedKey, afterDomain.SealedKey) || !bytes.Equal(m.SealedPayload, afterMail.SealedPayload) {
				t.Fatal("failed batch left partial re-encryption")
			}
			var auditCount int
			if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM internal_mail_operations WHERE id=$1", input.RequestID).Scan(&auditCount); err != nil || auditCount != 0 {
				t.Fatal("failed batch committed an audit result", err)
			}
			if failure == "fingerprint" {
				if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_messages SET payload_fingerprint=$2 WHERE id=$1", m.ID, m.PayloadFingerprint); err != nil {
					t.Fatal(err)
				}
			}
			changeEncryption(t, f, "rewrap", input)
		})
	}
}

func TestPGEncryptionMissingKeysAndStaleWritersPreserveQueuedMail(t *testing.T) {
	f, legacy, next := encryptionFixture(t)
	oldCodec := keyringForTest(t, legacy, legacy)
	stale := *f.service
	stale.Codec = oldCodec
	m := singleMail(t, f, "rotation-queue")
	changeEncryption(t, f, "activate", encryptionInput(EncryptionKeyID(next), 0, 0))
	if _, err := stale.Submit(t.Context(), "stale-owner", request()); !errors.Is(err, ErrEncryptionRevision) {
		t.Fatal("stale owner stored new legacy ciphertext", err)
	}
	if _, err := stale.RegisterDomain(t.Context(), "stale.example.test"); !errors.Is(err, ErrEncryptionRevision) {
		t.Fatal("stale domain writer escaped key selection", err)
	}
	stale.DeliveryEnabled = func() bool { return true }
	if err := stale.DeliverOne(t.Context()); !errors.Is(err, ErrEncryptionKeyUnavailable) {
		t.Fatal("worker without the active key claimed mail", err)
	}
	rewrapAll(t, f, EncryptionKeyID(next), 1)
	// Covers activation after a claim/readiness check: render cannot turn a
	// missing key into a terminal failure or spend a transport attempt.
	r, err := f.store.Claim(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	state, code := stale.prepareAndSubmit(t.Context(), r)
	if state != "queued" || code != "encryption_key_unavailable" {
		t.Fatal("missing key made mail terminal", state, code)
	}
	if err := f.store.Settle(t.Context(), r, state, code, time.Now()); err != nil {
		t.Fatal(err)
	}
	after, _ := f.store.Message(t.Context(), m.ID)
	if after.Recipients[0].Attempts != 0 || after.Recipients[0].Status != "queued" || len(f.transport.envelopes) != 0 {
		t.Fatal("missing key consumed attempts, changed custody or sent mail")
	}
	f.delivery.Store(true)
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal("repaired worker could not deliver", err)
	}
	if len(f.transport.envelopes) != 1 {
		t.Fatal("repaired worker did not make exactly one handoff")
	}
}

func TestPGEncryptionMigrationPreservesEveryCustodyStateAndNamespace(t *testing.T) {
	f, legacy, next := encryptionFixture(t)
	before := map[string]MessageResponse{}
	for _, state := range []string{"queued", "claimed", "submitting", "submitted", "uncertain", "deferred", "accepted"} {
		m := retentionMessage(t, f, "custody-key-"+state, state)
		m, _ = f.store.Message(t.Context(), m.ID)
		before[m.ID] = m
	}
	other, err := NewPGStore(f.pool, "isolated")
	if err != nil {
		t.Fatal(err)
	}
	otherCodec, _ := NewKeyringCodec("isolated", EncryptionKeyID(legacy), []*age.X25519Identity{legacy, next})
	otherService := *f.service
	otherService.Store, otherService.Codec = other, otherCodec
	otherDomain, err := otherService.RegisterDomain(t.Context(), "isolation.example.test")
	if err != nil {
		t.Fatal(err)
	}
	changeEncryption(t, f, "activate", encryptionInput(EncryptionKeyID(next), 0, 0))
	rewrapAll(t, f, EncryptionKeyID(next), 1)
	for id, before := range before {
		after, err := f.store.Message(t.Context(), id)
		if err != nil || !reflect.DeepEqual(before.Recipients, after.Recipients) || !before.CreatedAt.Equal(after.CreatedAt) {
			t.Fatal("migration changed recipient status, lease, attempts or timing", err)
		}
	}
	otherAfter, err := other.Domain(t.Context(), otherDomain.ID)
	if err != nil || !bytes.Equal(otherAfter.SealedKey, otherDomain.SealedKey) {
		t.Fatal("migration crossed namespace boundary", err)
	}
	otherStatus, err := other.encryptionStatus(t.Context(), otherCodec)
	if err != nil || otherStatus.Revision != 0 || otherStatus.Versioned {
		t.Fatal("key selection crossed namespaces", err)
	}
	changeEncryption(t, f, "retire", encryptionInput(EncryptionKeyID(legacy), 1, 0))
	if len(f.transport.envelopes) != 0 {
		t.Fatal("migration retried mail in unknown SMTP custody")
	}
}

func blockedEncryptionPID(t *testing.T, f *pgFixture, blocker uint32) int32 {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var pid int32
		err := f.pool.QueryRow(ctx, "SELECT pid FROM pg_stat_activity WHERE $1::integer=ANY(pg_blocking_pids(pid)) ORDER BY pid LIMIT 1", int32(blocker)).Scan(&pid)
		if err == nil {
			return pid
		}
		select {
		case <-ctx.Done():
			t.Fatal("expected writer/key fence never blocked")
		case <-ticker.C:
		}
	}
}

func TestPGEncryptionActivationWaitsForExistingOwnerAndFencesRacingOperators(t *testing.T) {
	f, _, next := encryptionFixture(t)
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackOperator(t.Context(), tx)
	if err := f.store.queries.LockNamespace(t.Context(), tx, "platform"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	ownerDone, rotateDone := make(chan error, 1), make(chan error, 1)
	go func() { _, err := f.service.Submit(ctx, "owner-before-key-fence", request()); ownerDone <- err }()
	ownerPID := blockedEncryptionPID(t, f, tx.Conn().PgConn().PID())
	go func() {
		_, err := f.service.ChangeEncryption(ctx, "activate", encryptionInput(EncryptionKeyID(next), 0, 0))
		rotateDone <- err
	}()
	blockedEncryptionPID(t, f, uint32(ownerPID))
	select {
	case err := <-rotateDone:
		t.Fatal("activation passed an existing encrypted owner", err)
	default:
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, done := range []chan error{ownerDone, rotateDone} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal("fenced owner or activation failed", err)
			}
		case <-ctx.Done():
			t.Fatal("key fence failed to drain")
		}
	}
	third, fourth := encryptionIdentity(t), encryptionIdentity(t)
	codec := f.service.Codec.(*KeyringCodec)
	newCodec, err := NewKeyringCodec("platform", codec.legacyID, []*age.X25519Identity{codec.keys[codec.legacyID], next, third, fourth})
	if err != nil || f.store.ConfigureEncryption(t.Context(), newCodec) != nil {
		t.Fatal("cannot stage racing selections", err)
	}
	f.service.Codec = newCodec
	var successes, conflicts atomic.Int32
	done := make(chan struct{}, 2)
	for _, id := range []string{EncryptionKeyID(third), EncryptionKeyID(fourth)} {
		go func() {
			defer func() { done <- struct{}{} }()
			_, err := f.service.ChangeEncryption(ctx, "activate", encryptionInput(id, 1, 0))
			if err == nil {
				successes.Add(1)
			} else if errors.Is(err, ErrEncryptionRevision) {
				conflicts.Add(1)
			}
		}()
	}
	for range 2 {
		select {
		case <-done:
		case <-ctx.Done():
			t.Fatal("racing activation stalled")
		}
	}
	if successes.Load() != 1 || conflicts.Load() != 1 {
		t.Fatal("racing operators both changed one selection revision")
	}
}

func TestPGEncryptionPrivateHTTPAndRollbackGuards(t *testing.T) {
	f, _, next := encryptionFixture(t)
	h, err := Handler(f.service, testToken)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, EncryptionPath, nil)
	r.Header.Set("Authorization", "Bearer "+operatorToken)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), EncryptionKeyID(next)) || strings.Contains(w.Body.String(), "AGE-SECRET") || strings.Contains(w.Body.String(), "identity_paths") {
		t.Fatal("private key inspection unavailable or leaked configuration", w.Code)
	}
	input := encryptionInput(EncryptionKeyID(next), 0, 0)
	body, _ := json.Marshal(input)
	r = httptest.NewRequest(http.MethodPost, EncryptionPath+"/activate", bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+operatorToken)
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), input.RequestID) {
		t.Fatal("operator activation failed", w.Code)
	}
	raw, err := migrations.FS.ReadFile("20261016170332382_internal_mail_encryption_rotation.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(raw), "-- +goose StatementBegin\n")
	down := strings.Split(parts[2], "-- +goose StatementEnd")[0]
	if _, err := f.pool.Exec(t.Context(), down); err == nil {
		t.Fatal("rollback erased selected identity or audit evidence")
	}
	if err := f.store.Ready(t.Context()); err != nil {
		t.Fatal("failed rollback changed schema readiness", err)
	}
	state, err := f.store.queries.EncryptionState(t.Context(), f.pool, "platform")
	if err != nil || state.ActiveKeyID != EncryptionKeyID(next) {
		t.Fatal("rejected rollback changed the selected writer", err)
	}
}

func TestPGEncryptionUnusedMigrationCanRollBackAndReapply(t *testing.T) {
	f, _, _ := encryptionFixture(t)
	m := singleMail(t, f, "before-key-schema")
	raw, err := migrations.FS.ReadFile("20261016170332382_internal_mail_encryption_rotation.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(raw), "-- +goose StatementBegin\n")
	up := strings.Split(parts[1], "-- +goose StatementEnd")[0]
	down := strings.Split(parts[2], "-- +goose StatementEnd")[0]
	for range 2 {
		if _, err := f.pool.Exec(t.Context(), down); err != nil {
			t.Fatal("unused schema rollback failed", err)
		}
	}
	if f.store.Ready(t.Context()) == nil {
		t.Fatal("missing encryption schema passed readiness")
	}
	for range 2 {
		if _, err := f.pool.Exec(t.Context(), up); err != nil {
			t.Fatal("migration reapplication failed", err)
		}
	}
	if err := f.store.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
	after, _ := f.store.Message(t.Context(), m.ID)
	if !bytes.Equal(m.SealedPayload, after.SealedPayload) || !bytes.Equal(m.PayloadFingerprint, after.PayloadFingerprint) || !reflect.DeepEqual(m.Recipients, after.Recipients) {
		t.Fatal("unused migration changed legacy mail")
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_messages SET payload_fingerprint=$2 WHERE id=$1", m.ID, make([]byte, 103)); err == nil {
		t.Fatal("malformed versioned verifier passed the database constraint")
	}
}
