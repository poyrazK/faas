package internalmail

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestPilotConfigurationFailsClosed(t *testing.T) {
	base := map[string]string{
		EnableEnv: "1", DeliveryEnableEnv: "1", "FAAS_INTERNAL_MAIL_TOKEN": testToken,
		"FAAS_INTERNAL_MAIL_AGE_IDENTITY_PATH": "/private/mail.age", "FAAS_INTERNAL_MAIL_SENDING_IP": "192.0.2.10",
		"FAAS_INTERNAL_MAIL_SMTP_ADDR": "127.0.0.1:2525", "FAAS_INTERNAL_MAIL_ALLOWED_RECIPIENTS": "one@example.net",
	}
	for _, tc := range []struct{ key, value string }{
		{"FAAS_INTERNAL_MAIL_ALLOWED_RECIPIENTS", ""},
		{"FAAS_INTERNAL_MAIL_ALLOWED_RECIPIENTS", "*@example.net"},
		{"FAAS_INTERNAL_MAIL_ALLOWED_RECIPIENTS", "example.net"},
		{"FAAS_INTERNAL_MAIL_ALLOWED_RECIPIENTS", "Person <one@example.net>"},
		{"FAAS_INTERNAL_MAIL_ALLOWED_RECIPIENTS", strings.Repeat("one@example.net,", api.InternalMailMaxPilotRecipients) + "two@example.net"},
		{"FAAS_INTERNAL_MAIL_DAILY_RECIPIENT_LIMIT", "0"},
		{"FAAS_INTERNAL_MAIL_DAILY_RECIPIENT_LIMIT", "101"},
		{"FAAS_INTERNAL_MAIL_DAILY_RECIPIENT_LIMIT", "invalid"},
	} {
		t.Run(tc.key+tc.value[:min(8, len(tc.value))], func(t *testing.T) {
			_, err := LoadConfig(func(key string) string {
				if key == tc.key {
					return tc.value
				}
				return base[key]
			})
			if err == nil {
				t.Fatal("unsafe pilot configuration accepted")
			}
		})
	}
	cfg, err := LoadConfig(func(key string) string { return base[key] })
	if err != nil || cfg.DailyRecipientLimit != 10 || !cfg.RecipientAllowed("one@example.net") || cfg.RecipientAllowed("two@example.net") {
		t.Fatal("invalid default pilot policy", err)
	}
}

func TestPGPilotRejectsUnapprovedAdmissionAndStopsWithdrawnRecipients(t *testing.T) {
	f := fixture(t)
	f.service.RecipientAllowed = func(address string) bool { return address == "one@example.net" }
	if _, err := f.service.Submit(t.Context(), "unapproved", request()); !errors.Is(err, ErrNotAllowed) {
		t.Fatal("mixed approved/unapproved submission admitted", err)
	}
	stats, err := f.store.Statistics(t.Context())
	if err != nil || stats.Recipients["queued"].Count != 0 {
		t.Fatal("rejected submission consumed queue capacity", err)
	}
	input := request()
	input.To = []string{"one@example.net"}
	m := submit(t, f, "approved-then-withdrawn", input)
	f.service.RecipientAllowed = func(string) bool { return false }
	f.delivery.Store(true)
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	after, err := f.store.Message(t.Context(), m.ID)
	if err != nil || after.Recipients[0].Status != "failed" || after.Recipients[0].Code != "recipient_not_allowed" || len(f.transport.envelopes) != 0 {
		t.Fatal("withdrawn mailbox received a submission", after, err)
	}
}

func TestPGPilotDailyBudgetIsAtomicAndIdempotent(t *testing.T) {
	f := fixture(t)
	store, err := NewPGStoreWithDailyLimit(f.pool, "platform", 2)
	if err != nil {
		t.Fatal(err)
	}
	f.service.Store = store
	input := request()
	input.To = []string{"one@example.net"}
	var wg sync.WaitGroup
	var mu sync.Mutex
	accepted := 0
	for _, key := range []string{"pilot-one", "pilot-two", "pilot-three", "pilot-four"} {
		wg.Go(func() {
			_, err := f.service.Submit(t.Context(), key, input)
			if err == nil {
				mu.Lock()
				accepted++
				mu.Unlock()
			} else if !errors.Is(err, ErrQuota) {
				t.Errorf("unexpected pilot admission error: %v", err)
			}
		})
	}
	wg.Wait()
	if accepted != 2 {
		t.Fatal("daily pilot budget exceeded", accepted)
	}
	stats, err := store.Statistics(t.Context())
	if err != nil || stats.Recipients["queued"].Count != 2 {
		t.Fatal(stats, err)
	}
	// Once admitted, an idempotent replay does not need another quota slot.
	for _, key := range []string{"pilot-one", "pilot-two", "pilot-three", "pilot-four"} {
		if _, err := f.service.Submit(t.Context(), key, input); err == nil {
			if _, err := f.service.Submit(t.Context(), key, input); err != nil {
				t.Fatal("idempotent replay consumed the remaining daily budget", err)
			}
			return
		}
	}
	t.Fatal("could not replay an admitted message")
}
