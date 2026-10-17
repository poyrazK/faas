package internalmail

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPGPostfixBridgeCommitsBeforeAcknowledgementAndDeduplicatesReplay(t *testing.T) {
	f := fixture(t)
	input := request()
	input.To = []string{"one@example.net"}
	m, err := f.service.Submit(t.Context(), "dsn-lost-response", input)
	if err != nil {
		t.Fatal(err)
	}
	f.delivery.Store(true)
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	// Stopping new submissions must still allow already queued relay reports.
	f.delivery.Store(false)
	var reports, dropResponse atomic.Bool
	reports.Store(true)
	dropResponse.Store(true)
	f.service.ReportsEnabled = reports.Load
	f.service.ReportToken = reportToken
	handler, err := Handler(f.service, testToken)
	if err != nil {
		t.Fatal(err)
	}
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
		// Commit the event, then lose the first response before Postfix can
		// acknowledge its DSN. A real pipe retry submits exactly these bytes.
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
	bridge, err := NewPostfixBridge(socket, reportToken, reports.Load)
	if err != nil {
		t.Fatal(err)
	}
	id := m.Recipients[0].ID
	to := "bounce+" + id + "@example.test"
	raw := sampleDSN(id, "relayed", "2.0.0")
	if err := bridge.Submit(t.Context(), "<>", to, bytes.NewReader(raw)); err == nil {
		t.Fatal("lost response acknowledged")
	}
	for range 2 {
		if err := bridge.Submit(t.Context(), "<>", to, bytes.NewReader(raw)); err != nil {
			t.Fatal(err)
		}
	}
	events, err := f.store.Events(t.Context(), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	observations := 0
	for _, event := range events {
		if strings.HasPrefix(event.ID, "postfix-dsn:") {
			observations++
		}
	}
	if observations != 1 {
		t.Fatal("duplicate durable report", observations)
	}
	// A late deferral, followed by a hard bounce and then a different success
	// notification, all commit evidence without regressing the final state.
	for _, data := range [][]byte{
		sampleDSN(id, "delayed", "4.4.1"), sampleDSN(id, "failed", "5.1.1"), sampleDSN(id, "delivered", "2.1.5"),
	} {
		if err := bridge.Submit(t.Context(), "<>", to, bytes.NewReader(data)); err != nil {
			t.Fatal(err)
		}
	}
	after, err := f.store.Message(t.Context(), m.ID)
	if err != nil || after.Recipients[0].Status != "bounced" || after.Recipients[0].Code != "dsn_5.1.1" {
		t.Fatal(after, err)
	}
	if suppressed, err := f.store.Suppressed(t.Context(), "one@example.net"); err != nil || !suppressed {
		t.Fatal("hard bounce was not suppressed", err)
	}
	// Mismatched final recipients cannot suppress a different address.
	bad := bytes.ReplaceAll(sampleDSN(id, "failed", "5.1.2"), []byte("one@example.net"), []byte("other@example.net"))
	if err := bridge.Submit(t.Context(), "<>", to, bytes.NewReader(bad)); err == nil {
		t.Fatal("accepted a mismatched final recipient")
	}
	if err := f.store.Report(t.Context(), Report{EventID: "genuine-feedback", RecipientID: id, Kind: "complained", Code: "feedback_loop"}); err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{sampleDSN(id, "failed", "5.2.1"), sampleDSN(id, "relayed", "2.1.5"), sampleDSN(id, "delayed", "4.4.2")} {
		if err := bridge.Submit(t.Context(), "<>", to, bytes.NewReader(data)); err != nil {
			t.Fatal(err)
		}
	}
	after, err = f.store.Message(t.Context(), m.ID)
	if err != nil || after.Recipients[0].Status != "complained" || after.Recipients[0].Code != "feedback_loop" {
		t.Fatal("late notification overwrote complaint", after, err)
	}
	reports.Store(false)
	r := httptest.NewRequest(http.MethodPost, PostfixReportPath, forbiddenRead{t})
	r.Header.Set("Authorization", "Bearer "+reportToken)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatal("disabled report route remained available")
	}
}

func TestPGPostfixReportWaitsForSubmissionSettlement(t *testing.T) {
	f := fixture(t)
	input := request()
	input.To = []string{"one@example.net"}
	m, err := f.service.Submit(t.Context(), "dsn-before-settle", input)
	if err != nil {
		t.Fatal(err)
	}
	r, err := f.store.Claim(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.BeginSubmission(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	report, err := ParsePostfixReport("bounce+"+r.ID+"@example.test", sampleDSN(r.ID, "delayed", "4.4.1"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.Report(t.Context(), report); err == nil {
		t.Fatal("raced the active submission lease")
	}
	if err := f.store.Settle(t.Context(), r, "submitted", "relay_accepted", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Report(t.Context(), report); err != nil {
		t.Fatal(err)
	}
	current, err := f.store.Message(t.Context(), m.ID)
	if err != nil || current.Recipients[0].Status != "submitted" {
		t.Fatal("relay deferral lost ownership", err)
	}
	if err := f.service.DeliverOne(t.Context()); err != nil || len(f.transport.envelopes) != 0 {
		t.Fatal("relay deferral caused another submission", err)
	}
}

func TestPGPostfixTemporaryQueueExpiryDoesNotSuppressMailbox(t *testing.T) {
	f := fixture(t)
	input := request()
	input.To = []string{"one@example.net"}
	m := submit(t, f, "dsn-expired-queue", input)
	f.delivery.Store(true)
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	id := m.Recipients[0].ID
	report, err := ParsePostfixReport("bounce+"+id+"@example.test", sampleDSN(id, "failed", "4.4.7"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.Report(t.Context(), report); err != nil {
		t.Fatal(err)
	}
	after, err := f.store.Message(t.Context(), m.ID)
	if err != nil || after.Recipients[0].Status != "bounced" {
		t.Fatal(after, err)
	}
	if suppressed, err := f.store.Suppressed(t.Context(), "one@example.net"); err != nil || suppressed {
		t.Fatal("temporary queue expiry permanently suppressed mailbox", err)
	}
}

func TestPGPostfixReportRecoversExpiredSubmissionWhileDeliveryIsPaused(t *testing.T) {
	f := fixture(t)
	input := request()
	input.To = []string{"one@example.net"}
	m := submit(t, f, "dsn-after-submission-crash", input)
	r, err := f.store.Claim(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.BeginSubmission(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_recipients SET lease_until=now()-interval '1 second' WHERE id=$1", r.ID); err != nil {
		t.Fatal(err)
	}
	report, err := ParsePostfixReport("bounce+"+r.ID+"@example.test", sampleDSN(r.ID, "relayed", "2.0.0"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.Report(t.Context(), report); err != nil {
		t.Fatal(err)
	}
	after, err := f.store.Message(t.Context(), m.ID)
	if err != nil || after.Recipients[0].Status != "accepted" || f.delivery.Load() {
		t.Fatal("could not reconcile while delivery paused", after, err)
	}
	events, err := f.store.Events(t.Context(), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	uncertain := 0
	for _, event := range events {
		if event.Kind == "uncertain" {
			uncertain++
		}
	}
	if uncertain != 1 {
		t.Fatal("missing audited lease recovery", events)
	}
	if err := f.store.Settle(t.Context(), r, "submitted", "late_worker", time.Now()); err == nil {
		t.Fatal("expired worker overwrote a reconciled report")
	}
}
