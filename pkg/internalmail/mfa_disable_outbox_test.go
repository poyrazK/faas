package internalmail

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMFADisableOutboxMaildTemplateGateIsIndependent(t *testing.T) {
	for _, template := range []string{ProducerOutboxMFADisableTemplate, ProducerOutboxVerificationTemplate, ProducerOutboxPasswordResetTemplate, ProducerOutboxMagicLinkTemplate, ProducerOutboxMFARecoveryAlertTemplate} {
		s := &Service{Enabled: func() bool { return true }, ProducerOutboxEnabled: func() bool { return true },
			MFADisableOutboxEnabled:       func() bool { return template != ProducerOutboxMFADisableTemplate },
			VerificationOutboxEnabled:     func() bool { return template != ProducerOutboxVerificationTemplate },
			PasswordResetOutboxEnabled:    func() bool { return template != ProducerOutboxPasswordResetTemplate },
			MagicLinkOutboxEnabled:        func() bool { return template != ProducerOutboxMagicLinkTemplate },
			MFARecoveryAlertOutboxEnabled: func() bool { return template != ProducerOutboxMFARecoveryAlertTemplate }}
		header := http.Header{}
		header.Set(ProducerOutboxTemplateHeader, template)
		header.Set(ProducerOutboxGenerationHeader, uuid.Nil.String())
		if _, err := s.producerOutboxContext(t.Context(), header); !errors.Is(err, ErrDisabled) {
			t.Fatal("enabled sibling admitted a disabled credential template", err)
		}
	}
}

func TestMFADisableOutboxTemplateHeaderRequiresExactGateAndGeneration(t *testing.T) {
	var gate atomic.Bool
	s := &Service{Enabled: func() bool { return true }, ProducersEnabled: func() bool { return true }, ProducerOutboxEnabled: func() bool { return true }, MFADisableOutboxEnabled: gate.Load,
		Producers: map[string]Producer{"auth": testProducer("auth", producerToken)}}
	h, err := Handler(s, testToken)
	if err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{false, true} {
		gate.Store(enabled)
		for _, headers := range []struct {
			templates  []string
			generation string
		}{
			{[]string{ProducerOutboxMFADisableTemplate}, uuid.Nil.String()},
			{[]string{ProducerOutboxMFADisableTemplate}, ""},
			{[]string{"unsupported"}, uuid.Nil.String()},
			{[]string{ProducerOutboxMFADisableTemplate, ProducerOutboxMFADisableTemplate}, uuid.Nil.String()},
		} {
			r := httptest.NewRequest(http.MethodPost, ProducersPath+"/auth/messages", forbiddenRead{t})
			r.Header.Set("Authorization", "Bearer "+producerToken)
			for _, v := range headers.templates {
				r.Header.Add(ProducerOutboxTemplateHeader, v)
			}
			if headers.generation != "" {
				r.Header.Set(ProducerOutboxGenerationHeader, headers.generation)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			want := http.StatusNotFound
			if enabled {
				want = http.StatusBadRequest
			} // nil store is never a generation-fenced backend.
			if w.Code != want {
				t.Fatal("mfa-disable header bypassed pre-body checks", w.Code)
			}
		}
	}
}

func TestPGMFADisableOutboxAdmissionRechecksTemplateGateAfterWait(t *testing.T) {
	f := producerFixture(t)
	var on atomic.Bool
	on.Store(true)
	f.service.ProducerOutboxEnabled = func() bool { return true }
	f.service.MFADisableOutboxEnabled = on.Load
	r := httptest.NewRequest(http.MethodPost, ProducersPath+"/auth/messages", nil)
	r.Header.Set(ProducerOutboxGenerationHeader, uuid.Nil.String())
	r.Header.Set(ProducerOutboxTemplateHeader, ProducerOutboxMFADisableTemplate)
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
		_, err := f.service.SubmitProduced(ctx, "auth", "late-disabled-mfa-disable", producerRequest())
		done <- err
	}()
	blockedEncryptionPID(t, f, tx.Conn().PgConn().PID())
	on.Store(false)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrDisabled) {
			t.Fatal("late template revocation admitted mail", err)
		}
	case <-ctx.Done():
		t.Fatal("admission did not leave namespace wait")
	}
	var count int
	if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM internal_mail_messages WHERE idempotency_key='late-disabled-mfa-disable'").Scan(&count); err != nil || count != 0 {
		t.Fatal("disabled mfa-disable admission persisted", err)
	}
}
