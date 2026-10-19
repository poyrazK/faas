package internalmail

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestDomainPacingDefaultOffAndConfigurationBounds(t *testing.T) {
	base := map[string]string{EnableEnv: "1", "FAAS_INTERNAL_MAIL_TOKEN": testToken, "FAAS_INTERNAL_MAIL_AGE_IDENTITY_PATH": "/private/mail.age", "FAAS_INTERNAL_MAIL_SENDING_IP": "192.0.2.10"}
	for _, value := range []string{"", "0", "true", "yes", "1 "} {
		base[PacingEnableEnv] = value
		cfg, err := LoadConfig(func(key string) string {
			if key == "FAAS_INTERNAL_MAIL_DOMAIN_PACING_INTERVAL_SECONDS" {
				t.Fatal("disabled pacing read policy")
			}
			return base[key]
		})
		if err != nil || cfg.PacingPolicy.Interval != 0 {
			t.Fatal("pacing was not default off", err)
		}
	}
	for _, master := range []string{"", "0", "true"} {
		if PacingEnabled(func(key string) string {
			if key == EnableEnv {
				return master
			}
			return "1"
		}) {
			t.Fatal("pacing did not require the master gate")
		}
	}
	base[PacingEnableEnv] = "1"
	get := func(key string) string { return base[key] }
	if cfg, err := LoadConfig(get); err != nil || cfg.PacingPolicy.Interval != api.InternalMailDomainPacingInterval {
		t.Fatal("pacing default interval missing", err)
	}
	for _, value := range []string{"0", "-1", "301", "1.5", "5s", " 1", strings.Repeat("9", 40)} {
		base["FAAS_INTERNAL_MAIL_DOMAIN_PACING_INTERVAL_SECONDS"] = value
		if _, err := LoadConfig(get); err == nil {
			t.Fatal("invalid pacing interval accepted", value)
		}
	}
	for _, value := range []string{"1", "300"} {
		base["FAAS_INTERNAL_MAIL_DOMAIN_PACING_INTERVAL_SECONDS"] = value
		if _, err := LoadConfig(get); err != nil {
			t.Fatal("bounded pacing interval rejected", err)
		}
	}
}

func TestDomainPacingRequiresDeliveryAndFencedStore(t *testing.T) {
	s := &Service{Enabled: func() bool { return true }, PacingEnabled: func() bool { return true }}
	if err := s.DeliverOne(t.Context()); err != nil {
		t.Fatal("delivery-off pacing accessed state", err)
	}
	s.DeliveryEnabled = func() bool { return true }
	if err := s.DeliverOne(t.Context()); !errors.Is(err, ErrInvalid) {
		t.Fatal("pacing accepted a store without a durable fence", err)
	}
	if err := s.beginMailSubmission(t.Context(), Recipient{}, ""); !errors.Is(err, ErrInvalid) {
		t.Fatal("pacing handoff accepted an unfenced store", err)
	}
	s.PacingPolicy.Interval = time.Nanosecond
	if _, err := s.claimMail(t.Context()); !errors.Is(err, ErrInvalid) {
		t.Fatal("invalid policy reached a store", err)
	}
}
