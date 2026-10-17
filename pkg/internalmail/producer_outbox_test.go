package internalmail

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/prometheus/client_golang/prometheus"
)

func TestProducerOutboxInspectionGateAndPrivateAuthentication(t *testing.T) {
	for _, flag := range []string{"", "0", "true", "1 "} {
		if ProducerOutboxEnabled(func(k string) string {
			if k == EnableEnv {
				return "1"
			}
			return flag
		}) {
			t.Fatal("nonexact gate enabled")
		}
	}
	if ProducerOutboxEnabled(func(k string) string {
		if k != EnableEnv {
			t.Fatal("disabled master read feature config")
		}
		return "0"
	}) {
		t.Fatal("disabled master enabled outbox")
	}
	var enabled atomic.Bool
	s := &Service{Enabled: func() bool { return true }, ProducerOutboxEnabled: enabled.Load, OperationsEnabled: func() bool { return true }, OperationsToken: operatorToken}
	h, err := Handler(s, testToken)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{ProducerOutboxPath, ProducerOutboxPath + "/items", ProducerOutboxPath + "/items/11111111-1111-4111-8111-111111111111"} {
		r := httptest.NewRequest(http.MethodGet, path, forbiddenRead{t})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 404 {
			t.Fatal("disabled outbox visible", w.Code)
		}
	}
	if _, err := s.ProducerOutboxCounts(t.Context()); !errors.Is(err, ErrDisabled) {
		t.Fatal("disabled service touched storage", err)
	}
	enabled.Store(true)
	for _, token := range []string{testToken, producerToken, reportToken, feedbackToken, ""} {
		r := httptest.NewRequest(http.MethodGet, ProducerOutboxPath+"/items", forbiddenRead{t})
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatal("nonoperator read outbox", w.Code)
		}
	}
	for _, query := range []string{"limit=0", "limit=101", "limit=1&limit=2", "after=bad", "after=%zz", "address=x", "status=submitted"} {
		r := httptest.NewRequest(http.MethodGet, ProducerOutboxPath+"/items?"+query, nil)
		r.Header.Set("Authorization", "Bearer "+operatorToken)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatal("invalid outbox query accepted", query, w.Code)
		}
	}
}

func TestProducerOutboxGenerationHeaderIsGatedAndValidatedBeforeBody(t *testing.T) {
	var enabled atomic.Bool
	s := &Service{Enabled: func() bool { return true }, ProducersEnabled: func() bool { return true },
		ProducerOutboxEnabled: enabled.Load, Producers: map[string]Producer{"auth": testProducer("auth", producerToken)}}
	h, err := Handler(s, testToken)
	if err != nil {
		t.Fatal(err)
	}
	for _, generation := range []string{uuid.Nil.String(), "not-a-uuid"} {
		r := httptest.NewRequest(http.MethodPost, ProducersPath+"/auth/messages", forbiddenRead{t})
		r.Header.Set("Authorization", "Bearer "+producerToken)
		r.Header.Set(ProducerOutboxGenerationHeader, generation)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 404 {
			t.Fatal("disabled generation transport visible", w.Code)
		}
	}
	enabled.Store(true)
	for _, values := range [][]string{{"invalid"}, {uuid.Nil.String(), uuid.Nil.String()}, {uuid.Nil.String()}} {
		r := httptest.NewRequest(http.MethodPost, ProducersPath+"/auth/messages", forbiddenRead{t})
		r.Header.Set("Authorization", "Bearer "+producerToken)
		for _, value := range values {
			r.Header.Add(ProducerOutboxGenerationHeader, value)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatal("malformed generation or unfenced store accepted", w.Code)
		}
	}
}

func TestPGProducerOutboxBoundAdmissionRechecksGateAfterOwnerWait(t *testing.T) {
	f := producerFixture(t)
	var enabled atomic.Bool
	enabled.Store(true)
	f.service.ProducerOutboxEnabled = enabled.Load
	r := httptest.NewRequest(http.MethodPost, ProducersPath+"/auth/messages", nil)
	r.Header.Set(ProducerOutboxGenerationHeader, uuid.Nil.String())
	bound, err := f.service.producerOutboxContext(r.Context(), r.Header)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackOperator(t.Context(), tx)
	if err := f.store.queries.LockNamespace(t.Context(), tx, "platform"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(bound, 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := f.service.SubmitProduced(ctx, "auth", "disabled-outbox-owner-wait", producerRequest())
		done <- err
	}()
	blockedEncryptionPID(t, f, tx.Conn().PgConn().PID())
	enabled.Store(false)
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrDisabled) {
			t.Fatal("late gate disable committed bound admission", err)
		}
	case <-ctx.Done():
		t.Fatal("admission did not leave owner wait")
	}
	var count int
	if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM internal_mail_messages WHERE idempotency_key='disabled-outbox-owner-wait'").Scan(&count); err != nil || count != 0 {
		t.Fatal("disabled bound admission persisted message", err)
	}
}

type outboxMetricSource struct {
	calls int
	fail  bool
}

func (s *outboxMetricSource) producerOutboxCounts(context.Context) ([]ProducerOutboxCount, error) {
	s.calls++
	if s.fail {
		return nil, errors.New("private producer diagnostic")
	}
	return []ProducerOutboxCount{{State: "queued", Total: 3, RecoveryHeld: 2}}, nil
}

func TestProducerOutboxMetricsAreBoundedGatedAndPrivate(t *testing.T) {
	source := &outboxMetricSource{}
	var enabled atomic.Bool
	r := prometheus.NewRegistry()
	r.MustRegister(NewProducerOutboxMonitor(source, enabled.Load))
	metrics, err := r.Gather()
	if err != nil || len(metrics) != 1 || source.calls != 0 {
		t.Fatal("disabled monitor read intents")
	}
	enabled.Store(true)
	metrics, err = r.Gather()
	if err != nil || len(metrics) != 10 {
		t.Fatal("missing aggregate metrics")
	}
	for _, family := range metrics {
		for _, metric := range family.Metric {
			if len(metric.Label) != 0 || strings.Contains(family.String(), "private producer diagnostic") {
				t.Fatal("private metric labels")
			}
		}
	}
	source.fail = true
	metrics, err = r.Gather()
	if err != nil || len(metrics) != 2 {
		t.Fatal("failed inventory reported counts")
	}
}
