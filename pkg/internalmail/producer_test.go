package internalmail

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

const producerToken = "private-producer-auth-test-token-32-bytes"
const secondProducerToken = "private-producer-billing-test-token-32-bytes"

func testProducer(id, token string) Producer {
	return Producer{ID: id, Token: token, From: []string{"notify@example.test"}, DailyRecipientLimit: 10, PendingRecipientLimit: 100}
}

func producerRequest() SendRequest {
	r := request()
	r.From = "Gregale <notify@example.test>"
	r.To = []string{"one@example.net"}
	return r
}

func TestProducersFlagAndDisabledRoutesReadNoInput(t *testing.T) {
	for _, tc := range []struct{ master, gate string }{{"", "1"}, {"true", "1"}, {"0", "1"}, {"1", ""}, {"1", "0"}, {"1", "true"}, {"1", "1 "}} {
		if ProducersEnabled(func(key string) string {
			if key == EnableEnv {
				return tc.master
			}
			return tc.gate
		}) {
			t.Fatal("non-exact producer opt-in enabled access")
		}
	}
	s := &Service{Enabled: func() bool { return true }}
	h, err := Handler(s, testToken)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{ProducersPath, ProducersPath + "/auth/messages", ProducersPath + "/auth/usage", ProducersPath + "/auth/messages/11111111-1111-4111-8111-111111111111/events"} {
		r := httptest.NewRequest(http.MethodPost, path, forbiddenRead{t})
		r.Header.Set("Authorization", "Bearer "+producerToken)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 404 {
			t.Fatal("disabled producer route visible", w.Code)
		}
	}
	if _, err := s.SubmitProduced(t.Context(), "auth", "key", SendRequest{}); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
	if _, err := s.ProducerMessage(t.Context(), "auth", "bad"); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
	if _, err := s.ProducerUsage(t.Context(), "auth"); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
}

func TestProducerEnabledRequiresFencedStore(t *testing.T) {
	s := &Service{Enabled: func() bool { return true }, ProducersEnabled: func() bool { return true }}
	if _, err := s.claimMail(t.Context()); !errors.Is(err, ErrInvalid) {
		t.Fatal("producer-enabled worker accepted a store without producer fences", err)
	}
	if err := s.beginMailSubmission(t.Context(), Recipient{}, ""); !errors.Is(err, ErrInvalid) {
		t.Fatal("producer-enabled handoff accepted a store without producer fences", err)
	}
}

func TestProducerConfigurationDefaultsAndStrictBoundaries(t *testing.T) {
	valid := testProducer("auth", producerToken)
	parse := func(raw string) (map[string]Producer, error) {
		return parseProducers(raw, testToken, reportToken, operatorToken, map[string]FeedbackSource{"provider": {Token: feedbackToken}})
	}
	raw, _ := json.Marshal([]map[string]any{{"id": valid.ID, "token": valid.Token, "from": valid.From}})
	configured, err := parse(string(raw))
	if err != nil || configured["auth"].DailyRecipientLimit != api.InternalMailProducerDailyRecipients || configured["auth"].PendingRecipientLimit != api.InternalMailProducerPending {
		t.Fatal("missing producer defaults", err)
	}
	for _, mutate := range []func(*Producer){
		func(p *Producer) { p.ID = "../auth" }, func(p *Producer) { p.ID = "Auth" },
		func(p *Producer) { p.ID = strings.Repeat("a", api.InternalMailMaxProducerIDBytes+1) },
		func(p *Producer) { p.Token = "short" }, func(p *Producer) { p.Token = testToken },
		func(p *Producer) { p.Token = reportToken }, func(p *Producer) { p.Token = operatorToken }, func(p *Producer) { p.Token = feedbackToken },
		func(p *Producer) { p.From = nil }, func(p *Producer) { p.From = []string{"*@example.test"} },
		func(p *Producer) { p.From = []string{"Name <notify@example.test>"} },
		func(p *Producer) { p.From = []string{"notify@EXAMPLE.test"} },
		func(p *Producer) { p.From = []string{valid.From[0], valid.From[0]} },
		func(p *Producer) { p.DailyRecipientLimit = -1 },
		func(p *Producer) { p.DailyRecipientLimit = 0 },
		func(p *Producer) { p.DailyRecipientLimit = api.InternalMailDailyRecipients + 1 },
		func(p *Producer) { p.PendingRecipientLimit = -1 },
		func(p *Producer) { p.PendingRecipientLimit = 0 },
		func(p *Producer) { p.PendingRecipientLimit = api.InternalMailMaxPending + 1 },
	} {
		p := valid
		mutate(&p)
		raw, _ := json.Marshal([]Producer{p})
		if _, err := parse(string(raw)); err == nil || strings.Contains(err.Error(), producerToken) {
			t.Fatal("invalid or leaking producer enrollment")
		}
	}
	duplicateID, _ := json.Marshal([]Producer{valid, valid})
	second := valid
	second.ID = "billing"
	duplicateToken, _ := json.Marshal([]Producer{valid, second})
	for _, invalid := range []string{"", "[]", "null", "[", string(duplicateID), string(duplicateToken), string(raw) + " {}", `[{"id":"auth","unknown":true}]`, strings.Repeat(" ", api.InternalMailMaxProducerConfigBytes+1)} {
		if _, err := parse(invalid); err == nil {
			t.Fatal("invalid producer config accepted")
		}
	}
}

func TestProducerConfigurationIsNotReadWhileDisabled(t *testing.T) {
	base := map[string]string{EnableEnv: "1", "FAAS_INTERNAL_MAIL_TOKEN": testToken, "FAAS_INTERNAL_MAIL_AGE_IDENTITY_PATH": "/private/mail.age", "FAAS_INTERNAL_MAIL_SENDING_IP": "192.0.2.10"}
	for _, value := range []string{"", "0", "true", "1 "} {
		base[ProducersEnableEnv] = value
		cfg, err := LoadConfig(func(key string) string {
			if key == "FAAS_INTERNAL_MAIL_PRODUCERS" {
				t.Fatal("disabled producers read private enrollment")
			}
			return base[key]
		})
		if err != nil || cfg.Producers != nil {
			t.Fatal("disabled producer enrollment loaded", err)
		}
	}
	base[ProducersEnableEnv] = "1"
	if _, err := LoadConfig(func(key string) string { return base[key] }); err == nil {
		t.Fatal("producer gate accepted empty enrollment")
	}
	raw, _ := json.Marshal([]Producer{testProducer("auth", producerToken)})
	base["FAAS_INTERNAL_MAIL_PRODUCERS"] = string(raw)
	if cfg, err := LoadConfig(func(key string) string { return base[key] }); err != nil || len(cfg.Producers) != 1 {
		t.Fatal("valid enrollment rejected", err)
	}
}
