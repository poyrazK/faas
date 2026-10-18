package internalmail

import (
	"bytes"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func enableFeedback(t *testing.T, f *pgFixture) feedbackSigner {
	t.Helper()
	signer := newFeedbackSigner(t)
	for name, records := range signer.resolver {
		f.resolver[name] = records
	}
	source := feedbackSource()
	f.service.FeedbackEnabled = func() bool { return true }
	f.service.FeedbackSources = map[string]FeedbackSource{source.ID: source}
	return signer
}

func submitFeedbackTarget(t *testing.T, f *pgFixture) MessageResponse {
	t.Helper()
	input := request()
	input.To = []string{"one@example.net"}
	m := submit(t, f, "feedback-target", input)
	f.delivery.Store(true)
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	f.delivery.Store(false)
	return m
}

func feedbackEventCount(t *testing.T, f *pgFixture, messageID string) int {
	t.Helper()
	events, err := f.store.Events(t.Context(), messageID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, event := range events {
		if strings.HasPrefix(event.ID, "feedback:provider:") {
			count++
			if strings.Contains(event.Code, "private") || strings.Contains(event.ID, "example.net") {
				t.Fatal("feedback audit retained raw report content")
			}
		}
	}
	return count
}

func TestPGFeedbackLateBounceDeduplicatesAcrossRestartAndSuppressesNewMail(t *testing.T) {
	f := fixture(t)
	signer := enableFeedback(t, f)
	m := submitFeedbackTarget(t, f)
	id := m.Recipients[0].ID
	if err := f.store.Report(t.Context(), Report{EventID: "mx-accepted", RecipientID: id, Kind: "accepted", Code: "smtp_250"}); err != nil {
		t.Fatal(err)
	}
	raw := signer.sign(t, sampleFeedback(id, "example.test", "one@example.net", "dsn", "5.1.1"), "feedback.provider.test", nil)
	var workers sync.WaitGroup
	for range 4 {
		workers.Go(func() {
			if err := f.service.IngestFeedback(t.Context(), "provider", raw); err != nil {
				t.Error(err)
			}
		})
	}
	workers.Wait()
	if n := feedbackEventCount(t, f, m.ID); n != 1 {
		t.Fatal("concurrent feedback was not deduplicated", n)
	}
	restarted, err := NewPGStore(f.pool, "platform")
	if err != nil {
		t.Fatal(err)
	}
	f.service.Store = restarted
	redelivered := append([]byte("Received: from durable-collector.example.test\r\n"), raw...)
	if err := f.service.IngestFeedback(t.Context(), "provider", redelivered); err != nil {
		t.Fatal("metadata changes broke replay", err)
	}
	if n := feedbackEventCount(t, f, m.ID); n != 1 {
		t.Fatal("metadata changes created another event", n)
	}
	after, err := restarted.Message(t.Context(), m.ID)
	if err != nil || after.Recipients[0].Status != "bounced" || after.Recipients[0].Code != "dsn_5.1.1" {
		t.Fatal("late bounce did not replace MX acceptance", after, err)
	}
	if suppressed, err := restarted.Suppressed(t.Context(), "one@example.net"); err != nil || !suppressed {
		t.Fatal("hard bounce was not suppressed", err)
	}
	contradictory := signer.sign(t, sampleFeedback(id, "example.test", "one@example.net", "dsn", "5.2.1"), "feedback.provider.test", nil)
	if err := f.service.IngestFeedback(t.Context(), "provider", contradictory); !errors.Is(err, ErrConflict) {
		t.Fatal("contradictory provider identity was reused", err)
	}
	input := request()
	input.To = []string{"one@example.net"}
	next := submit(t, f, "after-feedback", input)
	f.delivery.Store(true)
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	after, err = restarted.Message(t.Context(), next.ID)
	if err != nil || after.Recipients[0].Status != "suppressed" || len(f.transport.envelopes) != 1 {
		t.Fatal("suppressed mailbox was sent another message", after, err)
	}
}

func TestPGFeedbackComplaintsWinOverLateBounceAndDeferral(t *testing.T) {
	f := fixture(t)
	signer := enableFeedback(t, f)
	m := submitFeedbackTarget(t, f)
	id := m.Recipients[0].ID
	for _, raw := range [][]byte{
		sampleFeedback(id, "example.test", "one@example.net", "arf", ""),
		sampleFeedback(id, "example.test", "one@example.net", "dsn", "5.1.1"),
	} {
		if err := f.service.IngestFeedback(t.Context(), "provider", signer.sign(t, raw, "feedback.provider.test", nil)); err != nil {
			t.Fatal(err)
		}
	}
	after, err := f.store.Message(t.Context(), m.ID)
	if err != nil || after.Recipients[0].Status != "complained" || after.Recipients[0].Code != "arf_abuse" {
		t.Fatal("late bounce overwrote complaint", after, err)
	}
	if n := feedbackEventCount(t, f, m.ID); n != 2 {
		t.Fatal("feedback evidence was lost", n)
	}
	stats, err := f.store.Statistics(t.Context())
	if err != nil || stats.Suppressions != 1 || stats.Outcomes["complained"] != 1 || stats.Events["complained"] != 1 {
		t.Fatal("complaint was not reflected in durable monitoring", stats, err)
	}
}

func TestPGFeedbackRejectsWrongDomainRecipientAndNamespaceWithoutSideEffects(t *testing.T) {
	f := fixture(t)
	signer := enableFeedback(t, f)
	m := submitFeedbackTarget(t, f)
	id := m.Recipients[0].ID
	for _, tc := range []struct{ name, domain, to string }{
		{"wrong-sending-domain", "other.test", "one@example.net"},
		{"wrong-destination", "example.test", "two@example.net"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := signer.sign(t, sampleFeedback(id, tc.domain, tc.to, "arf", ""), "feedback.provider.test", nil)
			if err := f.service.IngestFeedback(t.Context(), "provider", raw); !errors.Is(err, ErrInvalid) {
				t.Fatal("uncorrelated report accepted", err)
			}
		})
	}
	other, err := NewPGStore(f.pool, "other")
	if err != nil {
		t.Fatal(err)
	}
	f.service.Store = other
	raw := signer.sign(t, sampleFeedback(id, "example.test", "one@example.net", "arf", ""), "feedback.provider.test", nil)
	if err := f.service.IngestFeedback(t.Context(), "provider", raw); !errors.Is(err, ErrNotFound) {
		t.Fatal("feedback crossed namespaces", err)
	}
	after, err := f.store.Message(t.Context(), m.ID)
	if err != nil || after.Recipients[0].Status != "submitted" || feedbackEventCount(t, f, m.ID) != 0 {
		t.Fatal("rejected feedback mutated original message", after, err)
	}
	if suppressed, err := f.store.Suppressed(t.Context(), "one@example.net"); err != nil || suppressed {
		t.Fatal("rejected report suppressed a mailbox", err)
	}
}

func TestPGFeedbackDeferralAndTemporaryQueueExpiryDoNotSuppressOrResend(t *testing.T) {
	f := fixture(t)
	signer := enableFeedback(t, f)
	m := submitFeedbackTarget(t, f)
	id := m.Recipients[0].ID
	raw := sampleFeedback(id, "example.test", "one@example.net", "dsn", "4.4.1")
	raw = bytes.Replace(raw, []byte("Action: failed"), []byte("Action: delayed"), 1)
	if err := f.service.IngestFeedback(t.Context(), "provider", signer.sign(t, raw, "feedback.provider.test", nil)); err != nil {
		t.Fatal(err)
	}
	after, err := f.store.Message(t.Context(), m.ID)
	if err != nil || after.Recipients[0].Status != "submitted" {
		t.Fatal("remote deferral transferred relay ownership", after, err)
	}
	failed := sampleFeedback(id, "example.test", "one@example.net", "dsn", "4.4.7")
	failed = bytes.Replace(failed, []byte("Message-ID: <feedback-"), []byte("Message-ID: <expiry-"), 1)
	if err := f.service.IngestFeedback(t.Context(), "provider", signer.sign(t, failed, "feedback.provider.test", nil)); err != nil {
		t.Fatal(err)
	}
	after, err = f.store.Message(t.Context(), m.ID)
	if err != nil || after.Recipients[0].Status != "bounced" {
		t.Fatal("final queue expiry was not recorded", after, err)
	}
	if suppressed, err := f.store.Suppressed(t.Context(), "one@example.net"); err != nil || suppressed {
		t.Fatal("temporary failure suppressed mailbox", err)
	}
	f.delivery.Store(true)
	if err := f.service.DeliverOne(t.Context()); err != nil || len(f.transport.envelopes) != 1 {
		t.Fatal("remote feedback resent an existing delivery", err)
	}
}

func TestPGFeedbackSubmissionRaceRetriesAndRecoversAfterLeaseExpiry(t *testing.T) {
	f := fixture(t)
	signer := enableFeedback(t, f)
	input := request()
	input.To = []string{"one@example.net"}
	m := submit(t, f, "submission-race", input)
	r, err := f.store.Claim(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.BeginSubmission(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	raw := signer.sign(t, sampleFeedback(r.ID, "example.test", r.Address, "dsn", "5.1.1"), "feedback.provider.test", nil)
	if err := f.service.IngestFeedback(t.Context(), "provider", raw); !errors.Is(err, ErrConflict) {
		t.Fatal("active submission was not fenced", err)
	}
	if n := feedbackEventCount(t, f, m.ID); n != 0 {
		t.Fatal("racing report was partially committed", n)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_recipients SET lease_until=now()-interval '1 second' WHERE id=$1", r.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.service.IngestFeedback(t.Context(), "provider", raw); err != nil {
		t.Fatal("feedback failed to reconcile expired submission", err)
	}
	after, err := f.store.Message(t.Context(), m.ID)
	if err != nil || after.Recipients[0].Status != "bounced" {
		t.Fatal(after, err)
	}
}

func TestPGFeedbackClientCommitsBeforeAcknowledgementAndReplaysLostResponse(t *testing.T) {
	f := fixture(t)
	signer := enableFeedback(t, f)
	m := submitFeedbackTarget(t, f)
	handler, err := Handler(f.service, testToken)
	if err != nil {
		t.Fatal(err)
	}
	var dropResponse atomic.Bool
	dropResponse.Store(true)
	socket := filepath.Join(t.TempDir(), "maild.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !dropResponse.Swap(false) {
			handler.ServeHTTP(w, r)
			return
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, r)
		if recorder.Code != http.StatusOK {
			t.Errorf("commit failed: HTTP %d", recorder.Code)
		}
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	client, err := NewFeedbackClient(socket, "provider", feedbackToken, func() bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	raw := signer.sign(t, sampleFeedback(m.Recipients[0].ID, "example.test", "one@example.net", "arf", ""), "feedback.provider.test", nil)
	if err := client.Submit(t.Context(), bytes.NewReader(raw)); err == nil {
		t.Fatal("lost response was acknowledged")
	}
	for range 2 {
		if err := client.Submit(t.Context(), bytes.NewReader(raw)); err != nil {
			t.Fatal(err)
		}
	}
	if n := feedbackEventCount(t, f, m.ID); n != 1 {
		t.Fatal("lost response produced duplicate events", n)
	}
	after, err := f.store.Message(t.Context(), m.ID)
	if err != nil || after.Recipients[0].Status != "complained" {
		t.Fatal("helper acknowledged without commit", after, err)
	}
}
