package internalmail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/internalmail/sqlc"
)

func lifecycleFixture(t *testing.T) *pgFixture {
	t.Helper()
	f := producerFixture(t)
	f.service.ProducerLifecycleEnabled = func() bool { return true }
	p := f.service.Producers["auth"]
	p.ReplacementToken = replacementProducerToken
	f.service.Producers["auth"] = p
	return f
}

func lifecycleRequest(f *pgFixture, action string, revision int64) ProducerLifecycleRequest {
	r := ProducerLifecycleRequest{OperatorAuditInput: auditInput(), ProducerID: "auth", ExpectedRevision: &revision}
	switch action {
	case "rotate":
		overlap := int32(api.InternalMailCredentialOverlap / time.Second)
		r.CredentialID, r.OverlapSeconds = f.service.producerCredentialConfig("auth").ReplacementID, &overlap
	case "revoke":
		r.CredentialID = f.service.producerCredentialConfig("auth").PrimaryID
	case "resume":
		r.ConfirmedReviewed = true
	}
	return r
}

func changeProducer(t *testing.T, f *pgFixture, action string, input ProducerLifecycleRequest) (ProducerAccessStatus, OperationRecord) {
	t.Helper()
	record, err := f.service.ChangeProducerAccess(t.Context(), action, input)
	if err != nil {
		t.Fatal("producer operation failed", action, err)
	}
	var status ProducerAccessStatus
	if json.Unmarshal(record.Result, &status) != nil {
		t.Fatal("invalid producer status result")
	}
	return status, record
}

func lifecycleHTTP(t *testing.T, f *pgFixture, method, path, token, key string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	r := httptest.NewRequest(method, path, reader)
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	h, err := Handler(f.service, testToken)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func credentialStatus(t *testing.T, status ProducerAccessStatus, id string) ProducerCredentialStatus {
	t.Helper()
	for _, c := range status.Credentials {
		if c.ID == id {
			return c
		}
	}
	t.Fatal("credential missing from status")
	return ProducerCredentialStatus{}
}

func TestPGProducerLifecycleRotationPreservesIdempotencyAndEventProgress(t *testing.T) {
	f := lifecycleFixture(t)
	f.service.EventFeedEnabled = func() bool { return true }
	messageRoute, _ := ProducerMessagesRoute("auth")
	usageRoute := ProducersPath + "/auth/usage"
	if w := lifecycleHTTP(t, f, http.MethodGet, usageRoute, replacementProducerToken, "", nil); w.Code != 401 {
		t.Fatal("unactivated replacement authenticated", w.Code)
	}
	first := lifecycleHTTP(t, f, http.MethodPost, messageRoute, producerToken, "stable-key", producerRequest())
	var before MessageResponse
	if first.Code != 202 || json.Unmarshal(first.Body.Bytes(), &before) != nil {
		t.Fatal("initial submission failed", first.Code)
	}
	if _, err := f.service.PublishEventFeed(t.Context()); err != nil {
		t.Fatal(err)
	}
	consumerRoute, _ := EventConsumerRoute("auth", "workflow")
	pulled := lifecycleHTTP(t, f, http.MethodPost, consumerRoute+"/pull", producerToken, "", map[string]int{"limit": 1})
	var batch EventFeedBatch
	if pulled.Code != 200 || json.Unmarshal(pulled.Body.Bytes(), &batch) != nil || len(batch.Events) != 1 || batch.Receipt == "" {
		t.Fatal("initial consumer pull failed", pulled.Code)
	}
	input := lifecycleRequest(f, "rotate", 0)
	status, rotation := changeProducer(t, f, "rotate", input)
	config := f.service.producerCredentialConfig("auth")
	old := credentialStatus(t, status, config.PrimaryID)
	if status.Revision != 1 || status.ActiveCredentialID != config.ReplacementID || old.RetiresAt == nil || !old.Usable || !old.RetiresAt.After(time.Now()) || old.RetiresAt.After(time.Now().Add(api.InternalMailCredentialOverlap)) {
		t.Fatal("rotation did not save a bounded overlap")
	}
	for _, token := range []string{producerToken, replacementProducerToken} {
		if w := lifecycleHTTP(t, f, http.MethodGet, usageRoute, token, "", nil); w.Code != 200 {
			t.Fatal("overlap interrupted caller access", w.Code)
		}
	}
	retried := lifecycleHTTP(t, f, http.MethodPost, messageRoute, replacementProducerToken, "stable-key", producerRequest())
	var after MessageResponse
	if retried.Code != 202 || json.Unmarshal(retried.Body.Bytes(), &after) != nil || after.ID != before.ID || after.ProducerID != before.ProducerID {
		t.Fatal("rotation changed producer ownership or message idempotency", retried.Code)
	}
	continued := lifecycleHTTP(t, f, http.MethodPost, consumerRoute+"/pull", replacementProducerToken, "", map[string]int{"limit": 100})
	var same EventFeedBatch
	if continued.Code != 200 || json.Unmarshal(continued.Body.Bytes(), &same) != nil || same.Receipt != batch.Receipt || same.Generation != batch.Generation || same.Events[0].ID != batch.Events[0].ID {
		t.Fatal("new credential lost the saved consumer batch", continued.Code)
	}
	ack := EventFeedAckInput{Receipt: batch.Receipt, Generation: batch.Generation}
	if w := lifecycleHTTP(t, f, http.MethodPost, consumerRoute+"/ack", replacementProducerToken, "", ack); w.Code != 200 {
		t.Fatal("replacement could not acknowledge existing progress", w.Code)
	}
	usage, err := f.service.ProducerUsage(t.Context(), "auth")
	if err != nil || usage.Daily != 1 || usage.Pending != 1 {
		t.Fatal("rotation charged admission again", err)
	}
	// Promote the activated secret in private config and remove the old secret.
	p := f.service.Producers["auth"]
	p.Token, p.ReplacementToken = p.ReplacementToken, ""
	f.service.Producers["auth"] = p
	restarted, _ := NewPGStore(f.pool, "platform")
	f.service.Store = restarted
	f.service.ProducerLifecycleEnabled = func() bool { return false }
	if w := lifecycleHTTP(t, f, http.MethodGet, usageRoute, replacementProducerToken, "", nil); w.Code != 200 {
		t.Fatal("promoted credential failed runtime rollback", w.Code)
	}
	f.service.ProducerLifecycleEnabled = func() bool { return true }
	_, replayed := changeProducer(t, f, "rotate", input)
	if !replayed.replayed || replayed.ID != rotation.ID || !bytes.Equal(replayed.Result, rotation.Result) {
		t.Fatal("response-loss replay required old secrets or extended overlap")
	}
	status, _ = changeProducer(t, f, "suspend", lifecycleRequest(f, "suspend", 1))
	_, replayed = changeProducer(t, f, "rotate", input)
	current, err := f.service.ProducerAccessStatus(t.Context(), "auth")
	if err != nil || !current.Suspended || current.Revision != status.Revision || !replayed.replayed {
		t.Fatal("saved rotation replay overwrote a later suspension", err)
	}
	var audit string
	if err := f.pool.QueryRow(t.Context(), "SELECT coalesce(string_agg(result::text,' '),'') FROM internal_mail_operations").Scan(&audit); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{producerToken, replacementProducerToken, operatorToken, "notify@", "one@", "Secret verification", "sealed_payload"} {
		if strings.Contains(audit, secret) {
			t.Fatal("lifecycle audit exposed credentials or message content")
		}
	}
}

func TestPGProducerLifecycleExpiryCannotBeUndoneByFlagConfigOrRestart(t *testing.T) {
	f := lifecycleFixture(t)
	status, _ := changeProducer(t, f, "rotate", lifecycleRequest(f, "rotate", 0))
	oldID := status.PrimaryCredentialID
	deadline := time.Now().Add(-time.Second).Truncate(time.Microsecond)
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_producer_credentials SET retires_at=$1 WHERE namespace='platform' AND credential_id=$2", deadline, oldID); err != nil {
		t.Fatal(err)
	}
	usageRoute := ProducersPath + "/auth/usage"
	for _, enabled := range []bool{true, false} {
		f.service.ProducerLifecycleEnabled = func() bool { return enabled }
		restarted, _ := NewPGStore(f.pool, "platform")
		f.service.Store = restarted
		if w := lifecycleHTTP(t, f, http.MethodGet, usageRoute, producerToken, "", nil); w.Code != 401 {
			t.Fatal("expired credential revived by rollback or restart", w.Code)
		}
	}
	p := f.service.Producers["auth"]
	p.Token, p.ReplacementToken = p.ReplacementToken, producerToken
	f.service.Producers["auth"] = p
	if w := lifecycleHTTP(t, f, http.MethodGet, usageRoute, replacementProducerToken, "", nil); w.Code != 200 {
		t.Fatal("promoted primary was tied to management gate", w.Code)
	}
	f.service.ProducerLifecycleEnabled = func() bool { return true }
	input := lifecycleRequest(f, "rotate", 1)
	if _, err := f.service.ChangeProducerAccess(t.Context(), "rotate", input); !errors.Is(err, ErrConflict) {
		t.Fatal("retired credential could be recycled", err)
	}
	current, err := f.service.ProducerAccessStatus(t.Context(), "auth")
	if err != nil || current.Revision != 1 || !credentialStatus(t, current, oldID).RetiresAt.Equal(deadline) {
		t.Fatal("failed rotation extended a saved deadline", err)
	}
}

func TestPGProducerLifecycleRevocationKeepsQueuedOwnershipAndSupportsRecovery(t *testing.T) {
	f := lifecycleFixture(t)
	m := produce(t, f, "auth", "before-revoke")
	status, _ := changeProducer(t, f, "revoke", lifecycleRequest(f, "revoke", 0))
	if status.Revision != 1 || credentialStatus(t, status, status.PrimaryCredentialID).RevokedAt == nil {
		t.Fatal("revocation was not durable")
	}
	f.service.ProducerLifecycleEnabled = func() bool { return false }
	h, _ := Handler(f.service, testToken)
	r := httptest.NewRequest(http.MethodPost, ProducersPath+"/auth/messages", forbiddenRead{t})
	r.Header.Set("Authorization", "Bearer "+producerToken)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("revoked credential read input or authenticated with management off", w.Code)
	}
	f.delivery.Store(true)
	if err := f.service.DeliverOne(t.Context()); err != nil || len(f.transport.envelopes) != 1 {
		t.Fatal("credential revocation changed queued mail delivery", err)
	}
	after, _ := f.store.Message(t.Context(), m.ID)
	if after.ProducerID != "auth" || after.Recipients[0].Status != "submitted" {
		t.Fatal("revocation erased ownership or relay custody")
	}
	f.service.ProducerLifecycleEnabled = func() bool { return true }
	status, _ = changeProducer(t, f, "rotate", lifecycleRequest(f, "rotate", 1))
	if w := lifecycleHTTP(t, f, http.MethodGet, ProducersPath+"/auth/usage", replacementProducerToken, "", nil); w.Code != 200 {
		t.Fatal("operator could not recover a revoked primary using a fresh staged key", w.Code)
	}
	input := lifecycleRequest(f, "revoke", status.Revision)
	input.CredentialID = status.ReplacementCredentialID
	changeProducer(t, f, "revoke", input)
	restarted, _ := NewPGStore(f.pool, "platform")
	f.service.Store = restarted
	if w := lifecycleHTTP(t, f, http.MethodGet, ProducersPath+"/auth/usage", replacementProducerToken, "", nil); w.Code != 401 {
		t.Fatal("revoked replacement authenticated after restart", w.Code)
	}
	if w := lifecycleHTTP(t, f, http.MethodGet, ProducersPath+"/billing/usage", secondProducerToken, "", nil); w.Code != 200 {
		t.Fatal("revocation crossed producer scope", w.Code)
	}
	other, _ := NewPGStore(f.pool, "other")
	if err := other.checkProducerAuthentication(t.Context(), producerAuthentication{ProducerID: "auth", CredentialID: producerCredentialID("auth", producerToken), PrimaryID: producerCredentialID("auth", producerToken)}); err != nil {
		t.Fatal("revocation crossed namespaces", err)
	}
}

func TestPGProducerLifecycleSuspensionFencesClaimsAndSurvivesDisabledManagement(t *testing.T) {
	f := lifecycleFixture(t)
	m := produce(t, f, "auth", "held-mail")
	r, err := f.service.claimMail(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	status, _ := changeProducer(t, f, "suspend", lifecycleRequest(f, "suspend", 0))
	if !status.Suspended || status.Revision != 1 {
		t.Fatal("suspension not saved")
	}
	f.service.ProducerLifecycleEnabled = func() bool { return false }
	if err := f.service.beginMailSubmission(t.Context(), r, f.domain.Selector); !errors.Is(err, ErrProducerSuspended) {
		t.Fatal("claimed handoff escaped durable suspension", err)
	}
	if err := f.store.Settle(t.Context(), r, "queued", producerSuspendedCode, time.Now()); err != nil {
		t.Fatal(err)
	}
	f.delivery.Store(true)
	codec := f.service.Codec
	f.service.Codec = nil
	if err := f.service.DeliverOne(t.Context()); err != nil || len(f.transport.envelopes) != 0 {
		t.Fatal("suspended producer was decrypted or sent", err)
	}
	f.service.Codec = codec
	after, _ := f.store.Message(t.Context(), m.ID)
	if after.Recipients[0].Status != "queued" || after.Recipients[0].Attempts != 0 || after.ProducerID != "auth" {
		t.Fatal("suspension spent an attempt or rewrote queued ownership")
	}
	if _, err := f.service.SubmitProduced(t.Context(), "auth", "new-mail", producerRequest()); !errors.Is(err, ErrProducerSuspended) {
		t.Fatal("suspended producer admitted mail", err)
	}
	produce(t, f, "billing", "other-producer")
	if err := f.service.DeliverOne(t.Context()); err != nil || len(f.transport.envelopes) != 1 {
		t.Fatal("suspension blocked a different producer", err)
	}
	f.service.ProducerLifecycleEnabled = func() bool { return true }
	if _, err := f.service.ChangeProducerAccess(t.Context(), "resume", lifecycleRequest(f, "resume", 0)); !errors.Is(err, ErrProducerRevision) {
		t.Fatal("stale revision resumed a producer", err)
	}
	changeProducer(t, f, "resume", lifecycleRequest(f, "resume", 1))
	if err := f.service.DeliverOne(t.Context()); err != nil || len(f.transport.envelopes) != 2 {
		t.Fatal("reviewed resume did not release existing mail", err)
	}
}

type blockingProducerBody struct {
	reader  io.Reader
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingProducerBody) Read(p []byte) (int, error) {
	b.once.Do(func() {
		close(b.entered)
		<-b.release
	})
	return b.reader.Read(p)
}

func TestPGProducerLifecycleRevocationRechecksAfterHTTPAuthentication(t *testing.T) {
	for _, replay := range []bool{false, true} {
		t.Run(map[bool]string{false: "new admission", true: "idempotency replay"}[replay], func(t *testing.T) {
			f := lifecycleFixture(t)
			if replay {
				produce(t, f, "auth", "racing-key")
			}
			raw, _ := json.Marshal(producerRequest())
			body := &blockingProducerBody{reader: bytes.NewReader(raw), entered: make(chan struct{}), release: make(chan struct{})}
			var once sync.Once
			release := func() { once.Do(func() { close(body.release) }) }
			defer release()
			h, err := Handler(f.service, testToken)
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(http.MethodPost, ProducersPath+"/auth/messages", body)
			r.Header.Set("Authorization", "Bearer "+producerToken)
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Idempotency-Key", "racing-key")
			w := httptest.NewRecorder()
			done := make(chan struct{})
			go func() { h.ServeHTTP(w, r); close(done) }()
			select {
			case <-body.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("request did not authenticate before reading input")
			}
			changeProducer(t, f, "revoke", lifecycleRequest(f, "revoke", 0))
			release()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("request did not finish after revocation")
			}
			if w.Code != 401 {
				t.Fatal("preauthenticated request escaped revocation", w.Code)
			}
			usage, err := f.service.ProducerUsage(t.Context(), "auth")
			want := int64(0)
			if replay {
				want = 1
			}
			if err != nil || usage.Daily != want || usage.Pending != want {
				t.Fatal("rejected admission changed durable mail or budgets", err)
			}
		})
	}
}

func TestPGProducerLifecycleMutationSerializesAndReplayCannotUndoRevocation(t *testing.T) {
	f := lifecycleFixture(t)
	requests := []ProducerLifecycleRequest{lifecycleRequest(f, "rotate", 0), lifecycleRequest(f, "rotate", 0)}
	var wg sync.WaitGroup
	results := make(chan OperationRecord, 2)
	for _, input := range requests {
		wg.Go(func() {
			record, err := f.service.ChangeProducerAccess(t.Context(), "rotate", input)
			if err != nil {
				if !errors.Is(err, ErrProducerRevision) {
					t.Error(err)
				}
				return
			}
			results <- record
		})
	}
	wg.Wait()
	close(results)
	var winner OperationRecord
	count := 0
	for record := range results {
		winner = record
		count++
	}
	if count != 1 {
		t.Fatal("racing operators both changed one revision", count)
	}
	var initial ProducerAccessStatus
	if json.Unmarshal(winner.Result, &initial) != nil {
		t.Fatal("invalid saved result")
	}
	input := lifecycleRequest(f, "revoke", 1)
	input.CredentialID = initial.ActiveCredentialID
	changeProducer(t, f, "revoke", input)
	for _, request := range requests {
		if request.RequestID != winner.ID {
			continue
		}
		record, err := f.service.ChangeProducerAccess(t.Context(), "rotate", request)
		if err != nil || !record.replayed || !bytes.Equal(record.Result, winner.Result) {
			t.Fatal("saved request replay failed", err)
		}
		request.Actor = "changed"
		if _, err := f.service.ChangeProducerAccess(t.Context(), "rotate", request); !errors.Is(err, ErrConflict) {
			t.Fatal("changed request reused saved audit identity", err)
		}
	}
	if w := lifecycleHTTP(t, f, http.MethodGet, ProducersPath+"/auth/usage", replacementProducerToken, "", nil); w.Code != 401 {
		t.Fatal("saved rotation replay undid a newer revocation", w.Code)
	}
}

func TestPGProducerLifecycleMigrationReplayAndRollbackGuard(t *testing.T) {
	f := lifecycleFixture(t)
	legacy := singleMail(t, f, "pre-lifecycle")
	raw, _ := migrations.FS.ReadFile("20261016152534774_internal_mail_producer_lifecycle.sql")
	parts := strings.Split(string(raw), "-- +goose StatementBegin\n")
	up := strings.Split(parts[1], "-- +goose StatementEnd")[0]
	down := strings.Split(parts[2], "-- +goose StatementEnd")[0]
	for range 2 {
		if _, err := f.pool.Exec(t.Context(), down); err != nil {
			t.Fatal(err)
		}
	}
	if f.store.Ready(t.Context()) == nil {
		t.Fatal("old schema passed lifecycle readiness")
	}
	for range 2 {
		if _, err := f.pool.Exec(t.Context(), up); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.store.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
	changeProducer(t, f, "suspend", lifecycleRequest(f, "suspend", 0))
	if _, err := f.pool.Exec(t.Context(), down); err == nil {
		t.Fatal("rollback erased durable suspension and audit")
	}
	after, err := f.store.Message(t.Context(), legacy.ID)
	if err != nil || after.Recipients[0].Status != "queued" {
		t.Fatal("migration changed legacy mail", err)
	}
	status, err := f.service.ProducerAccessStatus(t.Context(), "auth")
	if err != nil || !status.Suspended {
		t.Fatal("rejected rollback changed saved access", err)
	}
}

// An HTTP request carries this nonsecret authentication proof into the owner
// transaction. The Store still rechecks saved revocations and current time.
func producerAuthContext(ctx context.Context, f *pgFixture, token string) context.Context {
	return context.WithValue(ctx, producerAuthenticationKey{}, producerAuthentication{ProducerID: "auth", CredentialID: producerCredentialID("auth", token), PrimaryID: f.service.producerCredentialConfig("auth").PrimaryID})
}

func TestPGProducerLifecycleOverlapExpiryRollsBackAdmissionAndConsumerWrites(t *testing.T) {
	for _, operation := range []string{"admission", "replay", "pull", "ack"} {
		t.Run(operation, func(t *testing.T) {
			f := lifecycleFixture(t)
			f.service.EventFeedEnabled = func() bool { return true }
			if operation != "admission" {
				produce(t, f, "auth", "existing")
			}
			if _, err := f.service.PublishEventFeed(t.Context()); err != nil {
				t.Fatal(err)
			}
			var batch EventFeedBatch
			if operation == "ack" {
				var err error
				batch, err = f.service.PullEventFeed(t.Context(), "auth", "workflow", 1)
				if err != nil {
					t.Fatal(err)
				}
			}
			changeProducer(t, f, "rotate", lifecycleRequest(f, "rotate", 0))
			deadline := time.Now().Add(time.Second)
			if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_producer_credentials SET retires_at=$1 WHERE producer_id='auth' AND credential_id=$2", deadline, producerCredentialID("auth", producerToken)); err != nil {
				t.Fatal(err)
			}
			tx, err := f.pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer rollbackOperator(t.Context(), tx)
			if operation == "admission" || operation == "replay" {
				err = f.store.queries.LockNamespace(t.Context(), tx, "platform")
			} else {
				err = f.store.lockEventConsumers(t.Context(), tx, "auth")
			}
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(producerAuthContext(t.Context(), f, producerToken), 10*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				var err error
				switch operation {
				case "admission":
					_, err = f.service.SubmitProduced(ctx, "auth", "overlap-expired", producerRequest())
				case "replay":
					_, err = f.service.SubmitProduced(ctx, "auth", "existing", producerRequest())
				case "pull":
					_, err = f.service.PullEventFeed(ctx, "auth", "workflow", 1)
				case "ack":
					_, err = f.service.AckEventFeed(ctx, "auth", "workflow", EventFeedAckInput{Receipt: batch.Receipt, Generation: batch.Generation})
				}
				done <- err
			}()
			blockedEncryptionPID(t, f, tx.Conn().PgConn().PID())
			timer := time.NewTimer(max(time.Until(deadline)+time.Millisecond, 0))
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-done; !errors.Is(err, ErrProducerCredential) {
				t.Fatal("expired overlap committed owner writes", err)
			}
			usage, err := f.service.ProducerUsage(t.Context(), "auth")
			want := int64(1)
			if operation == "admission" {
				want = 0
			}
			if err != nil || usage.Daily != want || usage.Pending != want {
				t.Fatal("rejected owner request changed mail budgets", usage, err)
			}
		})
	}
}

func TestPGProducerLifecycleMutationWaitsForInFlightHandoff(t *testing.T) {
	f := lifecycleFixture(t)
	m := produce(t, f, "auth", "handoff-producer-fence")
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackOperator(t.Context(), tx)
	if _, err := f.store.queries.LockMailDomain(t.Context(), tx, q.LockMailDomainParams{Namespace: "platform", ID: pgID(m.DomainID)}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	f.delivery.Store(true)
	ownerDone := make(chan error, 1)
	go func() { ownerDone <- f.service.DeliverOne(ctx) }()
	ownerPID := blockedEncryptionPID(t, f, tx.Conn().PgConn().PID())
	mutated := make(chan error, 1)
	input := lifecycleRequest(f, "suspend", 0)
	go func() { _, err := f.service.ChangeProducerAccess(ctx, "suspend", input); mutated <- err }()
	blockedEncryptionPID(t, f, uint32(ownerPID))
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for _, done := range []chan error{ownerDone, mutated} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	after, err := f.store.Message(t.Context(), m.ID)
	if err != nil || after.Recipients[0].Status != "submitted" || len(f.transport.envelopes) != 1 {
		t.Fatal("producer mutation disrupted established relay custody", err)
	}
	if _, err := f.service.SubmitProduced(t.Context(), "auth", "after-suspend", producerRequest()); !errors.Is(err, ErrProducerSuspended) {
		t.Fatal("new admission crossed committed suspension", err)
	}
}

func TestPGProducerLifecyclePrivateHTTPRevokedStageAndImmediateCutover(t *testing.T) {
	f := lifecycleFixture(t)
	statusResponse := lifecycleHTTP(t, f, http.MethodGet, ProducerLifecyclePath+"/auth", operatorToken, "", nil)
	var initial ProducerAccessStatus
	if statusResponse.Code != 200 || json.Unmarshal(statusResponse.Body.Bytes(), &initial) != nil || initial.PrimaryCredentialID == "" || initial.ReplacementCredentialID == "" {
		t.Fatal("operator could not inspect staged identifiers", statusResponse.Code)
	}
	input := lifecycleRequest(f, "revoke", 0)
	input.CredentialID = initial.ReplacementCredentialID
	if w := lifecycleHTTP(t, f, http.MethodPost, ProducerLifecyclePath+"/revoke", operatorToken, "", input); w.Code != 200 {
		t.Fatal("operator could not revoke a staged credential", w.Code)
	}
	if w := lifecycleHTTP(t, f, http.MethodPost, ProducerLifecyclePath+"/rotate", operatorToken, "", lifecycleRequest(f, "rotate", 1)); w.Code != 409 {
		t.Fatal("revoked staged credential could be activated", w.Code)
	}
	freshToken := "second-private-auth-replacement-at-least-32-bytes"
	p := f.service.Producers["auth"]
	p.ReplacementToken = freshToken
	f.service.Producers["auth"] = p
	input = lifecycleRequest(f, "rotate", 1)
	zero := int32(0)
	input.OverlapSeconds = &zero
	w := lifecycleHTTP(t, f, http.MethodPost, ProducerLifecyclePath+"/rotate", operatorToken, "", input)
	if w.Code != 200 {
		t.Fatal("explicit immediate cutover failed", w.Code)
	}
	for _, token := range []string{producerToken, replacementProducerToken} {
		if w := lifecycleHTTP(t, f, http.MethodGet, ProducersPath+"/auth/usage", token, "", nil); w.Code != 401 {
			t.Fatal("retired or revoked secret survived immediate cutover", w.Code)
		}
	}
	if w := lifecycleHTTP(t, f, http.MethodGet, ProducersPath+"/auth/usage", freshToken, "", nil); w.Code != 200 {
		t.Fatal("fresh replacement could not authenticate after cutover", w.Code)
	}
	invalid := lifecycleRequest(f, "suspend", 2)
	invalid.ExpectedRevision = nil
	if w := lifecycleHTTP(t, f, http.MethodPost, ProducerLifecyclePath+"/suspend", operatorToken, "", invalid); w.Code != 400 {
		t.Fatal("HTTP accepted an unversioned mutation", w.Code)
	}
	status, err := f.service.ProducerAccessStatus(t.Context(), "auth")
	if err != nil || status.Revision != 2 || status.Suspended {
		t.Fatal("rejected HTTP request changed access", err)
	}
}

func TestPGProducerLifecycleGateRevocationDuringInputDeniesReplacementAdmission(t *testing.T) {
	f := lifecycleFixture(t)
	changeProducer(t, f, "rotate", lifecycleRequest(f, "rotate", 0))
	var enabled atomic.Bool
	enabled.Store(true)
	f.service.ProducerLifecycleEnabled = enabled.Load
	raw, _ := json.Marshal(producerRequest())
	body := &blockingProducerBody{reader: bytes.NewReader(raw), entered: make(chan struct{}), release: make(chan struct{})}
	var once sync.Once
	release := func() { once.Do(func() { close(body.release) }) }
	defer release()
	h, err := Handler(f.service, testToken)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, ProducersPath+"/auth/messages", body)
	r.Header.Set("Authorization", "Bearer "+replacementProducerToken)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", "gate-revoked")
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { h.ServeHTTP(w, r); close(done) }()
	select {
	case <-body.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("replacement did not authenticate")
	}
	enabled.Store(false)
	release()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not finish after gate revocation")
	}
	if w.Code != 401 {
		t.Fatal("replacement committed after its gate was revoked", w.Code)
	}
	usage, err := f.service.ProducerUsage(t.Context(), "auth")
	if err != nil || usage.Daily != 0 || usage.Pending != 0 {
		t.Fatal("gate-rejected request changed admission", err)
	}
}

func TestPGProducerLifecycleSuspendedFeedResumesItsSavedReceipt(t *testing.T) {
	f := lifecycleFixture(t)
	f.service.EventFeedEnabled = func() bool { return true }
	produce(t, f, "auth", "saved-progress")
	if _, err := f.service.PublishEventFeed(t.Context()); err != nil {
		t.Fatal(err)
	}
	batch, err := f.service.PullEventFeed(t.Context(), "auth", "workflow", 1)
	if err != nil || batch.Receipt == "" {
		t.Fatal(err)
	}
	changeProducer(t, f, "suspend", lifecycleRequest(f, "suspend", 0))
	f.service.ProducerLifecycleEnabled = func() bool { return false }
	if _, err := f.service.AckEventFeed(t.Context(), "auth", "workflow", EventFeedAckInput{Receipt: batch.Receipt, Generation: batch.Generation}); !errors.Is(err, ErrProducerSuspended) {
		t.Fatal("suspended consumer acknowledged a batch", err)
	}
	if _, err := f.service.PullEventFeed(t.Context(), "auth", "another-consumer", 1); !errors.Is(err, ErrProducerSuspended) {
		t.Fatal("suspended producer created a consumer", err)
	}
	f.service.ProducerLifecycleEnabled = func() bool { return true }
	changeProducer(t, f, "resume", lifecycleRequest(f, "resume", 1))
	same, err := f.service.PullEventFeed(t.Context(), "auth", "workflow", 100)
	if err != nil || same.Receipt != batch.Receipt || same.Generation != batch.Generation || same.Events[0].ID != batch.Events[0].ID {
		t.Fatal("suspension changed durable consumer progress", err)
	}
}

func TestPGProducerLifecycleRemovedEnrollmentStillAllowsAuditedRevocation(t *testing.T) {
	f := lifecycleFixture(t)
	status, _ := changeProducer(t, f, "suspend", lifecycleRequest(f, "suspend", 0))
	delete(f.service.Producers, "auth")
	current, err := f.service.ProducerAccessStatus(t.Context(), "auth")
	if err != nil || current.ActiveCredentialID != status.PrimaryCredentialID || current.PrimaryCredentialID != "" {
		t.Fatal("removed enrollment erased saved access identity", err)
	}
	input := lifecycleRequest(f, "revoke", 1)
	input.CredentialID = current.ActiveCredentialID
	status, _ = changeProducer(t, f, "revoke", input)
	if !status.Suspended || credentialStatus(t, status, input.CredentialID).RevokedAt == nil {
		t.Fatal("removed enrollment prevented permanent revocation")
	}
	f.service.Producers["auth"] = testProducer("auth", producerToken)
	changeProducer(t, f, "resume", lifecycleRequest(f, "resume", 2))
	if w := lifecycleHTTP(t, f, http.MethodGet, ProducersPath+"/auth/usage", producerToken, "", nil); w.Code != 401 {
		t.Fatal("re-enrollment revived a removed producer's revoked credential", w.Code)
	}
}

func TestPGProducerLifecycleRotationRecoversWithoutThePreviousSecret(t *testing.T) {
	f := lifecycleFixture(t)
	status, _ := changeProducer(t, f, "rotate", lifecycleRequest(f, "rotate", 0))
	previousActive := status.ActiveCredentialID
	// Both formerly configured secrets are absent. The saved identifier and
	// expected revision identify the old credential without recovering its value.
	p := f.service.Producers["auth"]
	p.Token = "new-private-config-primary-placeholder-at-least-32-bytes"
	p.ReplacementToken = "fresh-recovery-replacement-credential-at-least-32-bytes"
	f.service.Producers["auth"] = p
	input := lifecycleRequest(f, "rotate", 1)
	zero := int32(0)
	input.OverlapSeconds = &zero
	status, _ = changeProducer(t, f, "rotate", input)
	if status.ActiveCredentialID != input.CredentialID || status.Revision != 2 {
		t.Fatal("rotation required the previous secret")
	}
	var retired bool
	if err := f.pool.QueryRow(t.Context(), "SELECT retires_at<=clock_timestamp() FROM internal_mail_producer_credentials WHERE namespace='platform' AND producer_id='auth' AND credential_id=$1", previousActive).Scan(&retired); err != nil || !retired {
		t.Fatal("recovery did not retire the saved selected credential", err)
	}
	if w := lifecycleHTTP(t, f, http.MethodGet, ProducersPath+"/auth/usage", p.ReplacementToken, "", nil); w.Code != 200 {
		t.Fatal("fresh recovery credential did not authenticate", w.Code)
	}
	if w := lifecycleHTTP(t, f, http.MethodGet, ProducersPath+"/auth/usage", p.Token, "", nil); w.Code != 401 {
		t.Fatal("unselected configuration primary bypassed saved selection", w.Code)
	}
	// Even restoring the old secret as primary cannot revive its saved deadline.
	p.Token = replacementProducerToken
	f.service.Producers["auth"] = p
	f.service.ProducerLifecycleEnabled = func() bool { return false }
	if w := lifecycleHTTP(t, f, http.MethodGet, ProducersPath+"/auth/usage", replacementProducerToken, "", nil); w.Code != 401 {
		t.Fatal("recovery allowed a retired credential to be restored", w.Code)
	}
}
