package internalmail

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/prometheus/client_golang/prometheus"
)

func TestProducerSchedulingExactGateAndUnsupportedStore(t *testing.T) {
	for _, tc := range []struct{ master, flag string }{{"", "1"}, {"true", "1"}, {"1", ""}, {"1", "0"}, {"1", "true"}, {"1", "1 "}} {
		if ProducerSchedulingEnabled(func(key string) string {
			if key == EnableEnv {
				return tc.master
			}
			return tc.flag
		}) {
			t.Fatal("producer scheduler enabled without exact gates")
		}
	}
	s := &Service{Enabled: func() bool { return true }, ProducerSchedulingEnabled: func() bool { return true }}
	if err := s.DeliverOne(t.Context()); err != nil {
		t.Fatal("delivery-off scheduler accessed storage", err)
	}
	s.DeliveryEnabled = func() bool { return true }
	if err := s.DeliverOne(t.Context()); !errors.Is(err, ErrInvalid) {
		t.Fatal("scheduler accepted an unfenced store", err)
	}
	if err := s.beginMailSubmission(t.Context(), Recipient{}, ""); !errors.Is(err, ErrInvalid) {
		t.Fatal("handoff accepted an unfenced store", err)
	}
	policy, err := s.dispatchPolicy()
	if err != nil || len(policy.IDs) != 1 || policy.IDs[0] != "" || policy.Limits[0] != api.InternalMailProducerDispatch {
		t.Fatal("legacy bucket requires separate enrollment", policy, err)
	}
}

func TestProducerSchedulingConfigurationBounds(t *testing.T) {
	base := map[string]any{"id": "auth", "token": producerToken, "from": []string{"notify@example.test"}}
	for _, value := range []any{nil, 1, 100, 0, -1, 101, 1.5, "10"} {
		delete(base, "dispatch_per_minute")
		if value != nil {
			base["dispatch_per_minute"] = value
		}
		raw, _ := json.Marshal([]map[string]any{base})
		producers, err := parseProducers(string(raw), testToken, "", "", nil)
		valid := value == nil || value == 1 || value == 100
		if !valid && err == nil || valid && err != nil {
			t.Fatal("dispatch enrollment bounds", value, err)
		}
		if value == nil && producers["auth"].dispatchLimit() != api.InternalMailProducerDispatch {
			t.Fatal("missing conservative default")
		}
	}
	s := &Service{Enabled: func() bool { return true }, ProducerSchedulingEnabled: func() bool { return true }, ProducersEnabled: func() bool { return true }, Producers: map[string]Producer{"auth": testProducer("auth", producerToken)}}
	p := s.Producers["auth"]
	p.DispatchPerMinute = -1
	s.Producers["auth"] = p
	if _, err := s.dispatchPolicy(); !errors.Is(err, ErrInvalid) {
		t.Fatal("malformed runtime policy bypassed bounds", err)
	}
	for _, policy := range []DispatchPolicy{
		{Enabled: true}, {Enabled: true, IDs: []string{"auth"}, Limits: []int32{1}},
		{Enabled: true, IDs: []string{"", ""}, Limits: []int32{1, 1}},
		{Enabled: true, IDs: []string{""}, Limits: []int32{0}},
		{Enabled: true, IDs: []string{""}, Limits: []int32{101}},
		{IDs: []string{""}},
	} {
		if policy.valid() {
			t.Fatal("unsafe dispatch policy accepted", policy)
		}
	}
}

func TestProducerSchedulingPrivateRouteAndDisabledMetrics(t *testing.T) {
	var enabled atomic.Bool
	s := &Service{Enabled: func() bool { return true }, OperationsEnabled: func() bool { return true }, OperationsToken: operatorToken, ProducerSchedulingEnabled: enabled.Load}
	h, err := Handler(s, testToken)
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{ProducerSchedulerPath, ProducerSchedulerPath + "/unknown"} {
		r := httptest.NewRequest(http.MethodGet, route, forbiddenRead{t})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 404 {
			t.Fatal("disabled route revealed private scheduler", w.Code)
		}
	}
	registry := prometheus.NewRegistry()
	registry.MustRegister(NewProducerSchedulerMonitor(s))
	metrics, err := registry.Gather()
	if err != nil || len(metrics) != 1 || metrics[0].GetName() != "maild_mail_producer_scheduler_enabled" || metrics[0].Metric[0].GetGauge().GetValue() != 0 {
		t.Fatal("disabled monitoring accessed storage", metrics, err)
	}
	enabled.Store(true)
	for _, token := range []string{testToken, producerToken, reportToken, feedbackToken, ""} {
		r := httptest.NewRequest(http.MethodGet, ProducerSchedulerPath, forbiddenRead{t})
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatal("non-operator credential inspected dispatch", w.Code)
		}
	}
	metrics, err = registry.Gather()
	if err != nil || len(metrics) != 2 {
		t.Fatal("missing failed-read signal", err)
	}
	for _, family := range metrics {
		for _, metric := range family.Metric {
			if len(metric.Label) != 0 || strings.HasSuffix(family.GetName(), "stats_up") && metric.GetGauge().GetValue() != 0 {
				t.Fatal("unsafe metric or hidden read failure")
			}
		}
	}
}
