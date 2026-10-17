package internalmail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/migrations"
)

func reputationFixture(t *testing.T) (*pgFixture, feedbackSigner) {
	t.Helper()
	f := producerFixture(t)
	signer := enableFeedback(t, f)
	f.service.ReputationEnabled = func() bool { return true }
	for id, p := range f.service.Producers {
		p.DailyRecipientLimit = 100
		f.service.Producers[id] = p
	}
	return f, signer
}

func reputationMail(t *testing.T, f *pgFixture, producer, domain string) MessageResponse {
	t.Helper()
	key := uuid.NewString()
	r := request()
	r.From = "notify@" + domain
	r.To = []string{"r" + strings.ReplaceAll(key, "-", "") + "@example.net"}
	var m MessageResponse
	var err error
	if producer == "" {
		m, err = f.service.Submit(t.Context(), key, r)
	} else {
		m, err = f.service.SubmitProduced(t.Context(), producer, key, r)
	}
	if err != nil {
		t.Fatal(err)
	}
	return m
}
func reputationSamples(t *testing.T, f *pgFixture, n int) []MessageResponse {
	t.Helper()
	items := make([]MessageResponse, 0, n)
	for range n {
		items = append(items, reputationMail(t, f, "auth", "example.test"))
	}
	f.delivery.Store(true)
	for range n {
		if err := f.service.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	f.delivery.Store(false)
	return items
}
func reputationFeedback(t *testing.T, f *pgFixture, signer feedbackSigner, m MessageResponse, format, status, identity string) []byte {
	t.Helper()
	raw := sampleFeedback(m.Recipients[0].ID, "example.test", m.Recipients[0].Address, format, status)
	if identity != "" {
		raw = bytes.Replace(raw, []byte("Message-ID: <feedback-"), []byte("Message-ID: <"+identity+"-"), 1)
	}
	return signer.sign(t, raw, "feedback.provider.test", nil)
}
func ingestReputation(t *testing.T, f *pgFixture, signer feedbackSigner, m MessageResponse, format, status string) {
	t.Helper()
	if err := f.service.IngestFeedback(t.Context(), "provider", reputationFeedback(t, f, signer, m, format, status, "")); err != nil {
		t.Fatal(err)
	}
}
func reputationStatus(t *testing.T, f *pgFixture, scope ReputationScope) ReputationStatus {
	t.Helper()
	status, err := f.service.ReputationStatus(t.Context(), scope)
	if err != nil {
		t.Fatal(err)
	}
	return status
}
func reputationRelease(scope ReputationScope, revision int64) ReputationReleaseRequest {
	return ReputationReleaseRequest{OperatorAuditInput: auditInput(), ReputationScope: scope, ExpectedRevision: &revision, ConfirmedReviewed: true}
}

func TestPGReputationDurableScopeHoldsReviewReplayAndNewEvidence(t *testing.T) {
	f, signer := reputationFixture(t)
	items := reputationSamples(t, f, 24)
	for _, m := range items[:2] {
		ingestReputation(t, f, signer, m, "dsn", "5.1.1")
	}
	raw := reputationFeedback(t, f, signer, items[2], "dsn", "5.1.1", "")
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			if err := f.service.IngestFeedback(t.Context(), "provider", raw); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	scopes := []ReputationScope{{Kind: "producer", Key: "auth"}, {Kind: "domain", Key: f.domain.ID}}
	for _, scope := range scopes {
		status := reputationStatus(t, f, scope)
		if !status.Held || status.Revision != 1 || status.Reason != "bounce_rate" || status.Window.Samples != 24 || status.Window.Bounces != 3 || status.Evidence == nil {
			t.Fatal("bad hold evidence", status)
		}
	}
	// Producer and domain fences each block their own crossing of scopes.
	other, err := f.service.RegisterDomain(t.Context(), "other.test")
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range other.Records {
		f.resolver[record.Name] = []string{record.Value}
	}
	if _, err := f.service.VerifyDomain(t.Context(), other.ID); err != nil {
		t.Fatal(err)
	}
	for id, p := range f.service.Producers {
		p.From = append(p.From, "notify@other.test")
		f.service.Producers[id] = p
	}
	heldProducer := reputationMail(t, f, "auth", "other.test")
	heldDomain := reputationMail(t, f, "billing", "example.test")
	codec := f.service.Codec
	f.service.Codec = nil
	f.delivery.Store(true)
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal("held work decrypted", err)
	}
	f.service.Codec = codec
	unaffected := reputationMail(t, f, "billing", "other.test")
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(f.transport.envelopes) != 25 || f.transport.envelopes[24].RecipientID != unaffected.Recipients[0].ID {
		t.Fatal("holds escaped scope")
	}
	for _, m := range []MessageResponse{heldProducer, heldDomain} {
		after, _ := f.store.Message(t.Context(), m.ID)
		if after.Recipients[0].Attempts != 0 {
			t.Fatal("held work spent an attempt")
		}
	}
	store, _ := NewPGStore(f.pool, "platform")
	f.service.Store = store
	f.store = store
	for _, scope := range scopes {
		if !reputationStatus(t, f, scope).Held {
			t.Fatal("restart lost hold")
		}
	}
	request := reputationRelease(scopes[0], 1)
	first, err := f.service.ReleaseReputation(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := f.service.ReleaseReputation(t.Context(), request)
	if err != nil || !bytes.Equal(first.Result, replay.Result) {
		t.Fatal("lost response replay changed release", err)
	}
	changed := request
	changed.Evidence = "ticket:different"
	if _, err := f.service.ReleaseReputation(t.Context(), changed); !errors.Is(err, ErrConflict) {
		t.Fatal("changed replay accepted", err)
	}
	if !reputationStatus(t, f, scopes[1]).Held {
		t.Fatal("producer release cleared domain hold")
	}
	if _, err := f.service.ReleaseReputation(t.Context(), reputationRelease(scopes[1], 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.ReleaseReputation(t.Context(), reputationRelease(scopes[1], 1)); !errors.Is(err, ErrReputationRevision) {
		t.Fatal("stale revision released", err)
	}
	// Different provider identities for the same recipient cannot reopen a hold.
	if err := f.service.IngestFeedback(t.Context(), "provider", reputationFeedback(t, f, signer, items[2], "dsn", "5.1.1", "duplicate")); err != nil {
		t.Fatal(err)
	}
	f.delivery.Store(false)
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, scope := range scopes {
		if reputationStatus(t, f, scope).Held {
			t.Fatal("old evidence undid operator review")
		}
	}
	ingestReputation(t, f, signer, items[3], "dsn", "5.1.1")
	for _, scope := range scopes {
		status := reputationStatus(t, f, scope)
		if !status.Held || status.Revision != 3 {
			t.Fatal("new evidence failed to reopen", status)
		}
	}
	if replay, err := f.service.ReleaseReputation(t.Context(), request); err != nil || !bytes.Equal(replay.Result, first.Result) {
		t.Fatal("old release lost replay identity", err)
	}
	if status := reputationStatus(t, f, scopes[0]); !status.Held || status.Revision != 3 {
		t.Fatal("old replay cleared a newer hold", status)
	}
	page, err := store.reputationHolds(t.Context(), OperatorList{Limit: 1})
	if err != nil || len(page.Items) != 1 || page.Next == "" {
		t.Fatal("hold pagination", err)
	}
	last, err := store.reputationHolds(t.Context(), OperatorList{Limit: 1, After: page.Next})
	if err != nil || len(last.Items) != 1 || last.Items[0].cursor() == page.Items[0].cursor() {
		t.Fatal("hold cursor repeated", err)
	}
	events, err := store.reputationEvents(t.Context(), OperatorList{Limit: 100})
	if err != nil || len(events.Items) != 6 {
		t.Fatal("transition audit lost or duplicated", len(events.Items), err)
	}
	// Rates can age to zero; durable holds retain the original snapshot.
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_events SET created_at=clock_timestamp()-interval '25 hours'; UPDATE internal_mail_reputation_feedback SET bounced_at=clock_timestamp()-interval '25 hours'"); err != nil {
		t.Fatal(err)
	}
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	status := reputationStatus(t, f, scopes[0])
	if !status.Held || status.Window.Samples != 0 || status.Evidence.Bounces != 4 {
		t.Fatal("aged rate cleared durable evidence", status)
	}
}

func TestPGReputationWindowFloorsLateFeedbackAndComplaintPrecedence(t *testing.T) {
	f, signer := reputationFixture(t)
	items := reputationSamples(t, f, 19)
	for _, m := range items[:3] {
		ingestReputation(t, f, signer, m, "dsn", "5.1.1")
	}
	for _, m := range []MessageResponse{items[0], items[3]} {
		ingestReputation(t, f, signer, m, "arf", "")
	}
	scope := ReputationScope{Kind: "producer", Key: "auth"}
	status := reputationStatus(t, f, scope)
	if status.Held || status.Window.Samples != 19 || status.Window.Bounces != 2 || status.Window.Complaints != 2 {
		t.Fatal("sample floor or complaint priority failed", status)
	}
	// Crossing the floor without another bad report is evaluated by the worker.
	reputationMail(t, f, "auth", "example.test")
	f.delivery.Store(true)
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	f.delivery.Store(false)
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	status = reputationStatus(t, f, scope)
	if !status.Held || status.Reason != "complaint_rate" || status.Evidence.Samples != 20 {
		t.Fatal("worker missed rolling threshold", status)
	}
	if err := f.service.IngestFeedback(t.Context(), "provider", reputationFeedback(t, f, signer, items[0], "dsn", "5.1.1", "late-bounce")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_events SET created_at=clock_timestamp()-interval '25 hours'"); err != nil {
		t.Fatal(err)
	}
	status = reputationStatus(t, f, scope)
	if status.Window.Samples != 4 || status.Window.Bounces != 2 || status.Window.Complaints != 2 {
		t.Fatal("late feedback missing from denominator or double counted", status)
	}
	ingestReputation(t, f, signer, items[5], "dsn", "5.1.1")
	ingestReputation(t, f, signer, items[6], "dsn", "4.4.7")
	status = reputationStatus(t, f, scope)
	if status.Window.Samples != 5 || status.Window.Bounces != 3 || status.Window.Complaints != 2 {
		t.Fatal("hard/temporary bounce classification failed", status)
	}
	// A temporary queue expiry does not move the first custody clock or count as a hard bounce.
}

func TestPGReputationDuringRenderRefundsClaimAndPacing(t *testing.T) {
	f, signer := reputationFixture(t)
	items := reputationSamples(t, f, 24)
	for _, m := range items[:2] {
		ingestReputation(t, f, signer, m, "dsn", "5.1.1")
	}
	pending := reputationMail(t, f, "auth", "example.test")
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
		t.Fatal("worker did not render")
	}
	ingestReputation(t, f, signer, items[2], "dsn", "5.1.1")
	unblock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("worker did not settle")
	}
	after, _ := f.store.Message(ctx, pending.ID)
	r := after.Recipients[0]
	if r.Status != "queued" || r.Code != "reputation_held" || r.Attempts != 0 || len(f.transport.envelopes) != 24 || pacingRows(t, f) != 0 {
		t.Fatal("hold spent SMTP, attempt or pacing slot", r)
	}
	if result := cancellation(t, f, pending.ID, true); !result.Complete {
		t.Fatal("hold prevented cancellation")
	}
}

func TestPGReputationRateCrossesThresholdAsOldTrafficAges(t *testing.T) {
	f, signer := reputationFixture(t)
	items := reputationSamples(t, f, 61)
	for _, m := range items[:3] {
		ingestReputation(t, f, signer, m, "dsn", "5.1.1")
	}
	scope := ReputationScope{Kind: "producer", Key: "auth"}
	if reputationStatus(t, f, scope).Held {
		t.Fatal("rate below five percent caused hold")
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_events SET created_at=clock_timestamp()-interval '25 hours' WHERE recipient_id=$1", items[60].Recipients[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	status := reputationStatus(t, f, scope)
	if !status.Held || status.Evidence.Samples != 60 || status.Evidence.Bounces != 3 || status.Evidence.BounceRate != 0.05 {
		t.Fatal("aging window failed to trip hold", status)
	}
}

func TestPGReputationPreservesTimedExpiryAndUnknownCustody(t *testing.T) {
	f, signer := reputationFixture(t)
	items := reputationSamples(t, f, 24)
	for _, m := range items[:2] {
		ingestReputation(t, f, signer, m, "dsn", "5.1.1")
	}
	inflight := reputationMail(t, f, "auth", "example.test")
	r, err := f.service.claimMail(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.service.beginMailSubmission(t.Context(), r, f.domain.Selector); err != nil {
		t.Fatal(err)
	}
	f.service.SchedulingEnabled = func() bool { return true }
	expires := time.Now().Add(time.Hour)
	input := producerRequest()
	input.ExpiresAt = &expires
	timed, err := f.service.SubmitProduced(t.Context(), "auth", "reputation-expiry", input)
	if err != nil {
		t.Fatal(err)
	}
	ingestReputation(t, f, signer, items[2], "dsn", "5.1.1")
	if result := cancellation(t, f, inflight.ID, true); result.Complete || len(result.Protected) != 1 {
		t.Fatal("hold changed active custody")
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_messages SET created_at=clock_timestamp()-interval '1 minute',expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", timed.ID); err != nil {
		t.Fatal(err)
	}
	f.service.SchedulingEnabled = func() bool { return false }
	f.service.Codec = nil
	// Reputation maintenance enforces saved expiry even with SMTP and new scheduling disabled.
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	after, _ := f.store.Message(t.Context(), timed.ID)
	active, _ := f.store.Message(t.Context(), inflight.ID)
	if after.Recipients[0].Status != "expired" || after.Recipients[0].Attempts != 0 || active.Recipients[0].Status != "submitting" {
		t.Fatal("hold changed expiry or active custody")
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_recipients SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1", r.ID); err != nil {
		t.Fatal(err)
	}
	f.delivery.Store(true)
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	active, _ = f.store.Message(t.Context(), inflight.ID)
	if active.Recipients[0].Status != "uncertain" || len(f.transport.envelopes) != 24 {
		t.Fatal("held maintenance resent unknown custody")
	}
	if err := f.store.Report(t.Context(), Report{EventID: uuid.NewString(), RecipientID: r.ID, Kind: "accepted"}); err != nil {
		t.Fatal("hold blocked late evidence", err)
	}
}

func TestPGReputationFirstHoldSerializesWithSubmissionFence(t *testing.T) {
	f, signer := reputationFixture(t)
	items := reputationSamples(t, f, 24)
	for _, m := range items[:2] {
		ingestReputation(t, f, signer, m, "dsn", "5.1.1")
	}
	reputationMail(t, f, "auth", "example.test")
	r, err := f.service.claimMail(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackOperator(ctx, tx)
	if err := f.store.shareSubmissionControls(ctx, tx, r.ID); err != nil {
		t.Fatal(err)
	}
	raw := reputationFeedback(t, f, signer, items[2], "dsn", "5.1.1", "")
	done := make(chan error, 1)
	go func() { done <- f.service.IngestFeedback(ctx, "provider", raw) }()
	select {
	case err := <-done:
		t.Fatal("first hold bypassed active submission fence", err)
	case <-time.After(30 * time.Millisecond):
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("feedback remained blocked")
	}
	if err := f.service.beginMailSubmission(ctx, r, f.domain.Selector); !errors.Is(err, ErrReputationHeld) {
		t.Fatal("new handoff bypassed committed hold", err)
	}
}

func TestPGReputationRevokedFeedbackRollsBackReportAndHold(t *testing.T) {
	f, signer := reputationFixture(t)
	items := reputationSamples(t, f, 24)
	for _, m := range items[:2] {
		ingestReputation(t, f, signer, m, "dsn", "5.1.1")
	}
	calls := 0
	err := f.store.reportFeedback(t.Context(), Report{EventID: "fixture-third-bad", RecipientID: items[2].Recipients[0].ID, Kind: "bounced", Code: "dsn_5.1.1"}, func() bool { calls++; return calls == 1 })
	if !errors.Is(err, ErrDisabled) {
		t.Fatal("revoked feedback committed", err)
	}
	status := reputationStatus(t, f, ReputationScope{Kind: "producer", Key: "auth"})
	after, _ := f.store.Message(t.Context(), items[2].ID)
	if status.Held || status.Window.Bounces != 2 || after.Recipients[0].Status != "submitted" {
		t.Fatal("partial feedback commit", status)
	}
	ingestReputation(t, f, signer, items[2], "dsn", "5.1.1")
	request := reputationRelease(status.ReputationScope, 1)
	calls = 0
	if _, err := f.store.releaseReputation(t.Context(), request, func() bool { calls++; return calls == 1 }); !errors.Is(err, ErrDisabled) {
		t.Fatal("revoked release committed", err)
	}
	status = reputationStatus(t, f, status.ReputationScope)
	if !status.Held || status.Revision != 1 {
		t.Fatal("partial release")
	}
}

func TestPGReputationPrivateHTTPTrustNamespaceAndRuntimeRollback(t *testing.T) {
	f, signer := reputationFixture(t)
	items := reputationSamples(t, f, 24)
	// Authenticated service JSON reports are not provider authentication.
	for _, m := range items[:3] {
		if err := f.store.Report(t.Context(), Report{EventID: "feedback:forged:" + m.ID, RecipientID: m.Recipients[0].ID, Kind: "bounced", Code: "dsn_5.1.1"}); err != nil {
			t.Fatal(err)
		}
	}
	scope := ReputationScope{Kind: "producer", Key: "auth"}
	if status := reputationStatus(t, f, scope); status.Held || status.Window.Bounces != 0 {
		t.Fatal("report identity impersonated provider evidence")
	}
	for _, m := range items[:3] {
		ingestReputation(t, f, signer, m, "dsn", "5.1.1")
	}
	h, err := Handler(f.service, testToken)
	if err != nil {
		t.Fatal(err)
	}
	request := reputationRelease(scope, 1)
	request.ConfirmedReviewed = false
	raw, _ := json.Marshal(request)
	r := httptest.NewRequest(http.MethodPost, ReputationPath+"/release", bytes.NewReader(raw))
	r.Header.Set("Authorization", "Bearer "+operatorToken)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal("unconfirmed release accepted", w.Code)
	}
	request.ConfirmedReviewed = true
	raw, _ = json.Marshal(request)
	r = httptest.NewRequest(http.MethodPost, ReputationPath+"/release", bytes.NewReader(raw))
	r.Header.Set("Authorization", "Bearer "+operatorToken)
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "reputation_release") || strings.Contains(w.Body.String(), "Secret verification") {
		t.Fatal("private release response", w.Code, w.Body.String())
	}
	other, _ := NewPGStore(f.pool, "other")
	if status, err := other.reputationStatus(t.Context(), scope); err != nil || status.Held || status.Window.Samples != 0 {
		t.Fatal("hold crossed namespace", err)
	}
	if _, err := other.reputationStatus(t.Context(), ReputationScope{Kind: "domain", Key: f.domain.ID}); !errors.Is(err, ErrNotFound) {
		t.Fatal("domain crossed namespace", err)
	}
	if page, err := other.reputationEvents(t.Context(), OperatorList{Limit: 10}); err != nil || len(page.Items) != 0 {
		t.Fatal("audit crossed namespace", err)
	}
	pending := reputationMail(t, f, "auth", "example.test")
	controlChange(t, f, DeliveryScope{Kind: "namespace"}, "paused", 0)
	f.service.ReputationEnabled = func() bool { return false }
	f.delivery.Store(true)
	codec := f.service.Codec
	f.service.Codec = nil
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal("rollback bypassed manual hold", err)
	}
	controlChange(t, f, DeliveryScope{Kind: "namespace"}, "running", 1)
	f.service.Codec = codec
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if f.transport.envelopes[len(f.transport.envelopes)-1].RecipientID != pending.Recipients[0].ID {
		t.Fatal("flag rollback did not bypass automatic hold")
	}
	status, err := f.store.reputationStatus(t.Context(), ReputationScope{Kind: "domain", Key: f.domain.ID})
	if err != nil || !status.Held {
		t.Fatal("flag rollback discarded hold")
	}
}

func TestPGReputationMigrationReplayAndRollbackGuard(t *testing.T) {
	f, signer := reputationFixture(t)
	raw, _ := migrations.FS.ReadFile("20261016120104359_internal_mail_reputation.sql")
	parts := strings.Split(string(raw), "-- +goose StatementBegin\n")
	up := strings.Split(parts[1], "-- +goose StatementEnd")[0]
	down := strings.Split(parts[2], "-- +goose StatementEnd")[0]
	for range 2 {
		if _, err := f.pool.Exec(t.Context(), down); err != nil {
			t.Fatal(err)
		}
	}
	if f.store.Ready(t.Context()) == nil {
		t.Fatal("missing reputation schema passed readiness")
	}
	for range 2 {
		if _, err := f.pool.Exec(t.Context(), up); err != nil {
			t.Fatal(err)
		}
	}
	items := reputationSamples(t, f, 3)
	ingestReputation(t, f, signer, items[0], "dsn", "5.1.1")
	if _, err := f.pool.Exec(t.Context(), down); err == nil {
		t.Fatal("rollback erased authenticated feedback")
	}
	if err := f.store.Ready(t.Context()); err != nil {
		t.Fatal(fmt.Errorf("rollback damaged schema: %w", err))
	}
}
