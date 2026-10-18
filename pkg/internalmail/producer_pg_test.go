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

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/migrations"
)

func producerFixture(t *testing.T) *pgFixture {
	t.Helper()
	f := controlsFixture(t)
	f.service.ProducersEnabled = func() bool { return true }
	f.service.Producers = map[string]Producer{"auth": testProducer("auth", producerToken), "billing": testProducer("billing", secondProducerToken)}
	return f
}

func produce(t *testing.T, f *pgFixture, id, key string) MessageResponse {
	t.Helper()
	m, err := f.service.SubmitProduced(t.Context(), id, key, producerRequest())
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestPGProducerOwnershipConcurrentReplayAndCredentialRotation(t *testing.T) {
	f := producerFixture(t)
	var wg sync.WaitGroup
	ids := make(chan MessageResponse, 8)
	for _, id := range []string{"auth", "billing"} {
		for range 4 {
			wg.Go(func() {
				m, err := f.service.SubmitProduced(t.Context(), id, "shared-idempotency-key", producerRequest())
				if err != nil {
					t.Error(err)
					return
				}
				ids <- m
			})
		}
	}
	wg.Wait()
	close(ids)
	seen := map[string]string{}
	for m := range ids {
		if old := seen[m.ProducerID]; old != "" && old != m.ID {
			t.Fatal("producer retry created another message")
		}
		seen[m.ProducerID] = m.ID
	}
	legacy := submit(t, f, "shared-idempotency-key", producerRequest())
	if len(seen) != 2 || seen["auth"] == seen["billing"] || legacy.ID == seen["auth"] || legacy.ID == seen["billing"] {
		t.Fatal("idempotency crossed producer or legacy scopes")
	}
	for _, id := range []string{"auth", "billing"} {
		usage, err := f.service.ProducerUsage(t.Context(), id)
		if err != nil || usage.Daily != 1 || usage.Pending != 1 {
			t.Fatal("replay charged producer again", err)
		}
	}
	restarted, _ := NewPGStore(f.pool, "platform")
	f.service.Store = restarted
	p := f.service.Producers["auth"]
	p.Token = "new-private-auth-producer-credential-32-bytes"
	f.service.Producers["auth"] = p
	if m := produce(t, f, "auth", "shared-idempotency-key"); m.ID != seen["auth"] {
		t.Fatal("credential rotation lost message ownership")
	}
	changed := producerRequest()
	changed.Text = "changed"
	if _, err := f.service.SubmitProduced(t.Context(), "auth", "shared-idempotency-key", changed); !errors.Is(err, ErrConflict) {
		t.Fatal("contradictory producer replay accepted", err)
	}
	other, _ := NewPGStore(f.pool, "other")
	f.service.Store = other
	if _, err := f.service.ProducerMessage(t.Context(), "auth", seen["auth"]); !errors.Is(err, ErrNotFound) {
		t.Fatal("producer read crossed namespace", err)
	}
	if usage, err := f.service.ProducerUsage(t.Context(), "auth"); err != nil || usage.Daily != 0 {
		t.Fatal("producer budget crossed namespace", err)
	}
}

func TestPGProducerHTTPAuthorityAndOwnedMetadata(t *testing.T) {
	f := producerFixture(t)
	auth := produce(t, f, "auth", "private-idempotency-key")
	billing := produce(t, f, "billing", "billing-key")
	legacy := singleMail(t, f, "legacy-private")
	h, err := Handler(f.service, testToken)
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path, token string, body any) *httptest.ResponseRecorder {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", "http-key")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	base := ProducersPath + "/auth"
	if w := call(http.MethodPost, base+"/messages", producerToken, producerRequest()); w.Code != http.StatusAccepted {
		t.Fatal("scoped submission rejected", w.Code)
	}
	wrong := producerRequest()
	wrong.From = "unauthorized@example.test"
	f.service.Codec = nil // All remaining operations must need metadata only.
	if w := call(http.MethodPost, base+"/messages", producerToken, wrong); w.Code != http.StatusForbidden {
		t.Fatal("producer impersonated an unapproved sender", w.Code)
	}
	for _, path := range []string{base + "/messages/" + auth.ID, base + "/messages/" + auth.ID + "/events?limit=1", base + "/usage"} {
		w := call(http.MethodGet, path, producerToken, nil)
		if w.Code != http.StatusOK {
			t.Fatal("owned metadata unavailable", path, w.Code)
		}
		for _, secret := range []string{"sealed_payload", "fingerprint", "sealed_key", "lease_id", "private-idempotency-key", "123456", producerToken, secondProducerToken} {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatal("producer metadata leaked private content", secret)
			}
		}
	}
	for _, id := range []string{billing.ID, legacy.ID} {
		for _, suffix := range []string{"", "/events"} {
			if w := call(http.MethodGet, base+"/messages/"+id+suffix, producerToken, nil); w.Code != 404 {
				t.Fatal("producer accessed another message", w.Code)
			}
		}
	}
	for _, token := range []string{testToken, reportToken, operatorToken, feedbackToken, secondProducerToken} {
		if w := call(http.MethodGet, base+"/usage", token, nil); w.Code != 401 {
			t.Fatal("other credential entered producer routes", w.Code)
		}
	}
	for _, path := range []string{APIPath + "/domains", APIPath + "/messages", APIPath + "/reports", OperationsPath + "/messages"} {
		if w := call(http.MethodPost, path, producerToken, nil); w.Code != 401 {
			t.Fatal("producer acquired service or operator authority", path, w.Code)
		}
	}
	for _, path := range []string{RetentionPath, PostfixReportPath, FeedbackPath + "/provider"} {
		if w := call(http.MethodPost, path, producerToken, nil); w.Code != 401 && w.Code != 404 {
			t.Fatal("producer acquired maintenance/report authority", w.Code)
		}
	}
	if w := call(http.MethodGet, base+"/messages/"+auth.ID+"/events?status=queued", producerToken, nil); w.Code != 400 {
		t.Fatal("unrecognized producer event query accepted", w.Code)
	}
	if w := call(http.MethodPost, base+"/messages", producerToken, map[string]any{"producer_id": "billing"}); w.Code != 400 {
		t.Fatal("body-selected identity was accepted", w.Code)
	}
	p := f.service.Producers["auth"]
	p.Token = "rotated-private-auth-producer-token-32-bytes"
	f.service.Producers["auth"] = p
	if w := call(http.MethodGet, base+"/usage", producerToken, nil); w.Code != 401 {
		t.Fatal("old credential survived rotation", w.Code)
	}
	if w := call(http.MethodGet, base+"/messages/"+auth.ID, p.Token, nil); w.Code != 200 {
		t.Fatal("rotated credential lost its original messages", w.Code)
	}
	f.service.ProducersEnabled = func() bool { return false }
	if w := call(http.MethodGet, base+"/usage", producerToken, nil); w.Code != 404 {
		t.Fatal("disabled producer access remained available", w.Code)
	}
}

func TestPGProducerBudgetsAreAtomicAndPreserveNamespaceCap(t *testing.T) {
	f := producerFixture(t)
	store, _ := NewPGStoreWithDailyLimit(f.pool, "platform", 3)
	f.service.Store = store
	p := f.service.Producers["auth"]
	p.DailyRecipientLimit = 2
	f.service.Producers["auth"] = p
	var wg sync.WaitGroup
	accepted := make(chan string, 6)
	for range 6 {
		wg.Go(func() {
			key := uuid.NewString()
			_, err := f.service.SubmitProduced(t.Context(), "auth", key, producerRequest())
			if err == nil {
				accepted <- key
			} else if !errors.Is(err, ErrQuota) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	close(accepted)
	if len(accepted) != 2 {
		t.Fatal("producer budget oversubscribed")
	}
	for key := range accepted {
		m := produce(t, f, "auth", key)
		cancellation(t, f, m.ID, true)
	}
	if _, err := f.service.SubmitProduced(t.Context(), "auth", "after-cancel", producerRequest()); !errors.Is(err, ErrQuota) {
		t.Fatal("cancellation refunded daily producer usage", err)
	}
	produce(t, f, "billing", "other-budget")
	if _, err := f.service.SubmitProduced(t.Context(), "billing", "global-cap", producerRequest()); !errors.Is(err, ErrQuota) {
		t.Fatal("producer budgets bypassed namespace cap", err)
	}
	if usage, err := f.service.ProducerUsage(t.Context(), "auth"); err != nil || usage.Daily != 2 || usage.Pending != 0 {
		t.Fatal("incorrect durable producer usage", err)
	}
}

func TestPGProducerPendingBudgetAndExistingDispatchControls(t *testing.T) {
	f := producerFixture(t)
	p := f.service.Producers["auth"]
	p.PendingRecipientLimit = 1
	f.service.Producers["auth"] = p
	produce(t, f, "auth", "pending-first")
	if _, err := f.service.SubmitProduced(t.Context(), "auth", "pending-second", producerRequest()); !errors.Is(err, ErrQuota) {
		t.Fatal("pending producer cap bypassed", err)
	}
	scope := DeliveryScope{Kind: "namespace"}
	controlChange(t, f, scope, "paused", 0)
	f.delivery.Store(true)
	if err := f.service.DeliverOne(t.Context()); err != nil || len(f.transport.envelopes) != 0 {
		t.Fatal("producer bypassed namespace hold", err)
	}
	controlChange(t, f, scope, "running", 1)
	if err := f.service.DeliverOne(t.Context()); err != nil || len(f.transport.envelopes) != 1 {
		t.Fatal("resumed producer could not submit", err)
	}
	produce(t, f, "auth", "pending-second")
	f.service.RecipientAllowed = func(string) bool { return false }
	if _, err := f.service.SubmitProduced(t.Context(), "billing", "outside-pilot", producerRequest()); !errors.Is(err, ErrNotAllowed) {
		t.Fatal("producer bypassed global recipient allowlist", err)
	}
}

func TestPGProducerWithdrawalAndDisabledFlagDoNotClaimOrDecrypt(t *testing.T) {
	f := producerFixture(t)
	m := produce(t, f, "auth", "withdrawn")
	codec := f.service.Codec
	f.service.Codec = nil
	f.delivery.Store(true)
	f.service.ProducersEnabled = func() bool { return false }
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	f.service.ProducersEnabled = func() bool { return true }
	p := f.service.Producers["auth"]
	delete(f.service.Producers, "auth")
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	// Billing permits the same From mailbox, but cannot authorize auth's rows.
	after, _ := f.store.Message(t.Context(), m.ID)
	if after.Recipients[0].Attempts != 0 || len(f.transport.envelopes) != 0 {
		t.Fatal("disabled/withdrawn producer consumed claims")
	}
	p.From = []string{"replacement@example.test"}
	f.service.Producers["auth"] = p
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	f.service.Codec = codec
	produce(t, f, "billing", "unrelated-producer")
	singleMail(t, f, "unrelated-legacy")
	for range 2 {
		if err := f.service.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	after, _ = f.store.Message(t.Context(), m.ID)
	if after.Recipients[0].Attempts != 0 || len(f.transport.envelopes) != 2 {
		t.Fatal("withdrawn sender blocked other callers or was dispatched")
	}
	p.From = []string{"notify@example.test"}
	f.service.Producers["auth"] = p
	if err := f.service.DeliverOne(t.Context()); err != nil || len(f.transport.envelopes) != 3 {
		t.Fatal("restored producer failed dispatch", err)
	}
}

func TestPGProducerRevocationDuringRenderFencesSubmission(t *testing.T) {
	f := producerFixture(t)
	m := produce(t, f, "auth", "revoke-during-render")
	var enabled atomic.Bool
	enabled.Store(true)
	f.service.ProducersEnabled = enabled.Load
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
		t.Fatal("producer worker did not render")
	}
	enabled.Store(false)
	unblock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("revoked worker did not settle")
	}
	after, _ := f.store.Message(t.Context(), m.ID)
	if after.Recipients[0].Status != "queued" || after.Recipients[0].Attempts != 0 || len(f.transport.envelopes) != 0 {
		t.Fatal("revoked producer crossed submission or lost its attempt budget")
	}
}

func TestPGProducerAdmissionRevocationRollsBackAllRows(t *testing.T) {
	f := producerFixture(t)
	m := Message{ID: uuid.NewString(), DomainID: f.domain.ID, ProducerID: "auth", SenderAddress: "notify@example.test", IdempotencyKey: "revoked", SealedPayload: []byte("fixture")}
	r := Recipient{ID: uuid.NewString(), MessageID: m.ID, Address: "one@example.net"}
	var calls atomic.Int32
	_, _, err := f.store.enqueueProduced(t.Context(), m, []Recipient{r}, domainAuthorization{}, f.service.Producers["auth"], func() bool { return calls.Add(1) == 1 })
	if !errors.Is(err, ErrDisabled) {
		t.Fatal("revoked producer admission committed", err)
	}
	if _, err := f.store.Message(t.Context(), m.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("failed admission left a message", err)
	}
	if usage, err := f.service.ProducerUsage(t.Context(), "auth"); err != nil || usage.Daily != 0 || usage.Pending != 0 {
		t.Fatal("failed admission consumed producer budget", err)
	}
}

func TestPGProducerOwnershipSurvivesCancellationAndRetention(t *testing.T) {
	f := producerFixture(t)
	m := produce(t, f, "auth", "retained-replay")
	cancellation(t, f, m.ID, true)
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_messages SET created_at=now()-interval '8 days' WHERE id=$1", m.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_recipients SET updated_at=now()-interval '8 days' WHERE message_id=$1", m.ID); err != nil {
		t.Fatal(err)
	}
	if result, err := f.service.RetainPayloads(t.Context(), false); err != nil || result.PrunedMessages != 1 {
		t.Fatal("producer payload was not retained safely", err)
	}
	restarted, _ := NewPGStore(f.pool, "platform")
	f.service.Store = restarted
	replay := produce(t, f, "auth", "retained-replay")
	if replay.ID != m.ID || replay.Recipients[0].Status != "cancelled" || replay.ProducerID != "auth" || replay.SenderAddress != "notify@example.test" {
		t.Fatal("retention lost producer identity or revived cancelled work")
	}
	f.service.Codec = nil
	if owned, err := f.service.ProducerMessage(t.Context(), "auth", m.ID); err != nil || owned.PayloadPrunedAt == nil {
		t.Fatal("pruned metadata lost producer ownership", err)
	}
	if _, err := f.service.ProducerMessage(t.Context(), "billing", m.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("pruning widened message access", err)
	}
}

func TestPGProducerSubmissionCannotUseLegacyOrOtherProducerAuthority(t *testing.T) {
	f := producerFixture(t)
	produce(t, f, "auth", "claimed-producer")
	r, err := f.service.claimMail(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.BeginSubmission(t.Context(), r); !errors.Is(err, ErrLeaseLost) {
		t.Fatal("legacy store entrypoint could submit producer work", err)
	}
	if err := f.store.beginProducerSubmission(t.Context(), r, f.domain.Selector, domainAuthorization{}, []string{"billing:notify@example.test"}); !errors.Is(err, ErrLeaseLost) {
		t.Fatal("other producer's approved sender authorized handoff", err)
	}
	if err := f.service.beginMailSubmission(t.Context(), r, f.domain.Selector); err != nil {
		t.Fatal("correct producer authority could not start handoff", err)
	}
}

func TestPGProducerMigrationReplayAndOwnershipGuard(t *testing.T) {
	f := producerFixture(t)
	legacy := submit(t, f, "legacy-before-producers", producerRequest())
	raw, _ := migrations.FS.ReadFile("20261016095545574_internal_mail_producers.sql")
	parts := strings.Split(string(raw), "-- +goose StatementBegin\n")
	up := strings.Split(parts[1], "-- +goose StatementEnd")[0]
	down := strings.Split(parts[2], "-- +goose StatementEnd")[0]
	for range 2 {
		if _, err := f.pool.Exec(t.Context(), down); err != nil {
			t.Fatal(err)
		}
	}
	if f.store.Ready(t.Context()) == nil {
		t.Fatal("old schema passed producer readiness")
	}
	for range 2 {
		if _, err := f.pool.Exec(t.Context(), up); err != nil {
			t.Fatal(err)
		}
	}
	if replay := submit(t, f, "legacy-before-producers", producerRequest()); replay.ID != legacy.ID || replay.ProducerID != "" {
		t.Fatal("migration changed legacy identity or replay scope")
	}
	produce(t, f, "auth", "persistent-ownership")
	if _, err := f.pool.Exec(t.Context(), down); err == nil {
		t.Fatal("rollback discarded producer isolation")
	}
}
