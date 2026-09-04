package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

type invoicePDFRequesterStub struct {
	mu       sync.Mutex
	calls    int
	started  chan struct{}
	finished chan struct{}
	err      error
}

func (s *invoicePDFRequesterStub) RequestInvoicePDF(context.Context, string) error {
	s.mu.Lock()
	s.calls++
	calls := s.calls
	s.mu.Unlock()
	if calls == 1 {
		close(s.started)
	}
	if s.err != nil {
		return s.err
	}
	close(s.finished)
	return nil
}

func (s *invoicePDFRequesterStub) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

var _ billing.InvoicePDFRequester = (*invoicePDFRequesterStub)(nil)

func TestBillingWebhooksRejectInactiveProvider(t *testing.T) {
	for _, tc := range []struct {
		name string
		hand func(*server, http.ResponseWriter, *http.Request)
	}{
		{name: "polar", hand: func(s *server, w http.ResponseWriter, r *http.Request) { s.polarWebhook(w, r) }},
		{name: "paddle", hand: func(s *server, w http.ResponseWriter, r *http.Request) { s.paddleWebhook(w, r) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := &server{
				billingProvider: &fakeBillingProvider{},
				log:             slog.New(slog.NewTextHandler(io.Discard, nil)),
			}
			req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/"+tc.name, nil)
			rec := httptest.NewRecorder()
			tc.hand(srv, rec, req)
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503; body=%s", rec.Code, rec.Body)
			}
		})
	}
}

func TestRequestPolarInvoicePDFAsyncDoesNotBlockCaller(t *testing.T) {
	requester := &invoicePDFRequesterStub{
		started:  make(chan struct{}),
		finished: make(chan struct{}),
		err:      errors.New("temporary provider error"),
	}
	srv := &server{log: slog.New(slog.NewTextHandler(io.Discard, nil))}

	started := time.Now()
	srv.requestPolarInvoicePDFAsync(context.Background(), requester, "order-1", "event-1")
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("async helper blocked caller for %s", elapsed)
	}
	select {
	case <-requester.started:
	case <-time.After(time.Second):
		t.Fatal("invoice PDF worker did not start")
	}
	deadline := time.After(3 * time.Second)
	for requester.Calls() < 3 {
		select {
		case <-deadline:
			t.Fatalf("invoice PDF worker calls = %d, want 3", requester.Calls())
		case <-time.After(25 * time.Millisecond):
		}
	}
}

type blockingBillingMailer struct {
	started  chan struct{}
	release  chan struct{}
	finished chan struct{}
}

func (m *blockingBillingMailer) Send(ctx context.Context, _ Message) error {
	close(m.started)
	select {
	case <-m.release:
		close(m.finished)
		return nil
	case <-ctx.Done():
		close(m.finished)
		return ctx.Err()
	}
}

func TestPolarBillingTransitionMailDoesNotBlockWebhookPath(t *testing.T) {
	mailer := &blockingBillingMailer{
		started:  make(chan struct{}),
		release:  make(chan struct{}),
		finished: make(chan struct{}),
	}
	srv := &server{
		mailer: mailer,
		log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	startedAt := time.Now()
	srv.sendBillingTransitionMail(context.Background(), state.Account{ID: "acct-1", Email: "alice@example.com"}, "payment failed", "body", true, "payment_failed")
	if elapsed := time.Since(startedAt); elapsed > 100*time.Millisecond {
		t.Fatalf("Polar mail helper blocked caller for %s", elapsed)
	}
	select {
	case <-mailer.started:
	case <-time.After(time.Second):
		t.Fatal("Polar mail worker did not start")
	}
	close(mailer.release)
	select {
	case <-mailer.finished:
	case <-time.After(time.Second):
		t.Fatal("Polar mail worker did not finish")
	}
}
