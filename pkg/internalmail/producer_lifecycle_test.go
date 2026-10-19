package internalmail

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

const replacementProducerToken = "private-auth-replacement-credential-at-least-32-bytes"

func TestProducerLifecycleExactGateAndPrerequisites(t *testing.T) {
	for _, tc := range []struct{ master, flag string }{{"", "1"}, {"true", "1"}, {"1", ""}, {"1", "0"}, {"1", "true"}, {"1", "1 "}} {
		if ProducerLifecycleEnabled(func(key string) string {
			if key == EnableEnv {
				return tc.master
			}
			return tc.flag
		}) {
			t.Fatal("producer lifecycle enabled without exact gates")
		}
	}
	base := map[string]string{EnableEnv: "1", ProducerLifecycleEnableEnv: "1", "FAAS_INTERNAL_MAIL_TOKEN": testToken, "FAAS_INTERNAL_MAIL_AGE_IDENTITY_PATH": "/private/mail.age", "FAAS_INTERNAL_MAIL_SENDING_IP": "192.0.2.10"}
	if _, err := LoadConfig(func(key string) string {
		if key == "FAAS_INTERNAL_MAIL_PRODUCERS" {
			t.Fatal("missing prerequisite read producer secrets")
		}
		return base[key]
	}); err == nil {
		t.Fatal("lifecycle started without producers")
	}
	s := &Service{Enabled: func() bool { return true }}
	if _, err := s.ProducerAccessStatus(t.Context(), "bad"); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
	if _, err := s.ChangeProducerAccess(t.Context(), "bad", ProducerLifecycleRequest{}); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
	s.ProducerLifecycleEnabled = func() bool { return true }
	if err := s.DeliverOne(t.Context()); err != nil {
		t.Fatal("SMTP-off worker accessed producer state", err)
	}
	s.DeliveryEnabled = func() bool { return true }
	if err := s.DeliverOne(t.Context()); !errors.Is(err, ErrInvalid) {
		t.Fatal("worker accepted an unfenced store", err)
	}
	if err := s.beginMailSubmission(t.Context(), Recipient{}, ""); !errors.Is(err, ErrInvalid) {
		t.Fatal("submission accepted an unfenced store", err)
	}
}

func TestProducerLifecycleReplacementConfigurationIsDistinctAcrossRoles(t *testing.T) {
	p := testProducer("auth", producerToken)
	parse := func(producers []Producer) error {
		raw, _ := json.Marshal(producers)
		_, err := parseProducers(string(raw), testToken, reportToken, operatorToken, map[string]FeedbackSource{"provider": {Token: feedbackToken}})
		return err
	}
	for _, token := range []string{p.Token, testToken, reportToken, operatorToken, feedbackToken, "short", "bad token with whitespace and at least 32 bytes"} {
		p.ReplacementToken = token
		if err := parse([]Producer{p}); err == nil {
			t.Fatal("unsafe replacement credential accepted")
		}
	}
	p.ReplacementToken = replacementProducerToken
	if err := parse([]Producer{p}); err != nil {
		t.Fatal("distinct staged replacement rejected", err)
	}
	other := testProducer("billing", replacementProducerToken)
	if err := parse([]Producer{p, other}); err == nil {
		t.Fatal("replacement credential collided with another producer")
	}
	other.Token = secondProducerToken
	other.ReplacementToken = p.Token
	if err := parse([]Producer{p, other}); err == nil {
		t.Fatal("primary credential collided with another replacement")
	}
	if producerCredentialID("auth", producerToken) == producerCredentialID("billing", producerToken) || !validCredentialID(producerCredentialID("auth", producerToken)) {
		t.Fatal("credential identifiers did not bind producer ownership")
	}
}

func TestProducerLifecycleRoutesGateBeforeCredentialsAndBody(t *testing.T) {
	var enabled atomic.Bool
	p := testProducer("auth", producerToken)
	p.ReplacementToken = replacementProducerToken
	s := &Service{Enabled: func() bool { return true }, ProducersEnabled: func() bool { return true }, Producers: map[string]Producer{"auth": p}, OperationsEnabled: func() bool { return true }, OperationsToken: operatorToken, ProducerLifecycleEnabled: enabled.Load}
	h, err := Handler(s, testToken)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{ProducerLifecyclePath, ProducerLifecyclePath + "/auth", ProducerLifecyclePath + "/rotate", ProducerLifecyclePath + "/suspend", ProducerLifecyclePath + "/revoke"} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			r := httptest.NewRequest(method, path, forbiddenRead{t})
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 404 {
				t.Fatal("disabled lifecycle route visible", path, w.Code)
			}
		}
	}
	r := httptest.NewRequest(http.MethodPost, ProducersPath+"/auth/messages", forbiddenRead{t})
	r.Header.Set("Authorization", "Bearer "+replacementProducerToken)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("staged replacement bypassed its gate", w.Code)
	}
	enabled.Store(true)
	for _, token := range []string{testToken, producerToken, replacementProducerToken, reportToken, feedbackToken, ""} {
		r := httptest.NewRequest(http.MethodPost, ProducerLifecyclePath+"/revoke", forbiddenRead{t})
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatal("non-operator credential managed a producer", w.Code)
		}
	}
}

func TestProducerLifecycleRequestBounds(t *testing.T) {
	revision, overlap := int64(0), int32(3600)
	valid := ProducerLifecycleRequest{OperatorAuditInput: auditInput(), ProducerID: "auth", ExpectedRevision: &revision, CredentialID: producerCredentialID("auth", replacementProducerToken), OverlapSeconds: &overlap}
	if err := valid.Validate("rotate"); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*ProducerLifecycleRequest){
		func(r *ProducerLifecycleRequest) { r.ExpectedRevision = nil },
		func(r *ProducerLifecycleRequest) { v := int64(-1); r.ExpectedRevision = &v },
		func(r *ProducerLifecycleRequest) { r.OverlapSeconds = nil },
		func(r *ProducerLifecycleRequest) { v := int32(-1); r.OverlapSeconds = &v },
		func(r *ProducerLifecycleRequest) { v := int32(86401); r.OverlapSeconds = &v },
		func(r *ProducerLifecycleRequest) { r.ProducerID = "../auth" },
		func(r *ProducerLifecycleRequest) { r.CredentialID = strings.Repeat("A", 64) },
		func(r *ProducerLifecycleRequest) { r.CredentialID = producerToken },
		func(r *ProducerLifecycleRequest) { r.ConfirmedReviewed = true },
	} {
		input := valid
		mutate(&input)
		if input.Validate("rotate") == nil {
			t.Fatal("unsafe rotation request accepted")
		}
	}
	valid.CredentialID, valid.OverlapSeconds = "", nil
	if valid.Validate("resume") == nil || valid.Validate("suspend") != nil || valid.Validate("unknown") == nil {
		t.Fatal("suspension or review guard failed")
	}
	valid.ConfirmedReviewed = true
	if valid.Validate("resume") != nil || valid.Validate("suspend") == nil {
		t.Fatal("reviewed resume invalid")
	}
}

type producerLifecycleStatsFunc func(context.Context) (ProducerLifecycleStatistics, error)

func (f producerLifecycleStatsFunc) producerLifecycleStatistics(ctx context.Context) (ProducerLifecycleStatistics, error) {
	return f(ctx)
}

func TestProducerLifecycleMetricsDefaultOffFailureAndClosedLabels(t *testing.T) {
	var enabled, failed atomic.Bool
	calls := 0
	registry := prometheus.NewRegistry()
	registry.MustRegister(NewProducerLifecycleMonitor(producerLifecycleStatsFunc(func(context.Context) (ProducerLifecycleStatistics, error) {
		calls++
		if failed.Load() {
			return ProducerLifecycleStatistics{}, ErrInvalid
		}
		return ProducerLifecycleStatistics{SuspendedProducers: 1, RevokedCredentials: 2, RetiringCredentials: 3, ExpiredCredentials: 4}, nil
	}), enabled.Load))
	metrics, err := registry.Gather()
	if err != nil || len(metrics) != 1 || calls != 0 || metrics[0].Metric[0].GetGauge().GetValue() != 0 {
		t.Fatal("disabled metrics read credential state", err)
	}
	enabled.Store(true)
	metrics, err = registry.Gather()
	if err != nil || len(metrics) != 6 || calls != 1 {
		t.Fatal("missing access aggregates", err)
	}
	for _, family := range metrics {
		if len(family.Metric) != 1 || len(family.Metric[0].Label) != 0 {
			t.Fatal("credential identity leaked in metric labels")
		}
	}
	failed.Store(true)
	metrics, err = registry.Gather()
	if err != nil || len(metrics) != 2 || calls != 2 {
		t.Fatal("missing failed-read signal", err)
	}
	for _, family := range metrics {
		if strings.HasSuffix(family.GetName(), "stats_up") && family.Metric[0].GetGauge().GetValue() != 0 {
			t.Fatal("failed read claimed success")
		}
	}
}
