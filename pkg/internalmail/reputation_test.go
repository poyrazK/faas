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

func TestReputationDefaultOffAndRequiresAuthenticatedFeedback(t *testing.T) {
	for _, tc := range []struct{ master, flag string }{{"", "1"}, {"true", "1"}, {"1", ""}, {"1", "0"}, {"1", "true"}, {"1", "1 "}} {
		if ReputationEnabled(func(k string) string {
			if k == EnableEnv {
				return tc.master
			}
			return tc.flag
		}) {
			t.Fatal("reputation enabled without exact opt-in")
		}
	}
	s := &Service{Enabled: func() bool { return true }}
	if _, err := s.ReputationStatus(t.Context(), ReputationScope{}); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
	if _, err := s.ReleaseReputation(t.Context(), ReputationReleaseRequest{}); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
	if err := s.DeliverOne(t.Context()); err != nil {
		t.Fatal("disabled worker accessed state", err)
	}
	s.ReputationEnabled = func() bool { return true }
	if err := s.DeliverOne(t.Context()); !errors.Is(err, ErrInvalid) {
		t.Fatal("maintenance accepted unsupported store", err)
	}
	s.DeliveryEnabled = func() bool { return true }
	s.FeedbackEnabled = func() bool { return true }
	if err := s.DeliverOne(t.Context()); !errors.Is(err, ErrInvalid) {
		t.Fatal("dispatch accepted unfenced store", err)
	}
	if err := s.beginMailSubmission(t.Context(), Recipient{}, ""); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	base := map[string]string{EnableEnv: "1", ReputationEnableEnv: "1", "FAAS_INTERNAL_MAIL_TOKEN": testToken, "FAAS_INTERNAL_MAIL_AGE_IDENTITY_PATH": "/private/mail.age", "FAAS_INTERNAL_MAIL_SENDING_IP": "192.0.2.10"}
	if _, err := LoadConfig(func(k string) string { return base[k] }); err == nil {
		t.Fatal("reputation started without feedback")
	}
	sources, _ := json.Marshal([]FeedbackSource{feedbackSource()})
	base[FeedbackEnableEnv], base["FAAS_INTERNAL_MAIL_FEEDBACK_SOURCES"] = "1", string(sources)
	if _, err := LoadConfig(func(k string) string { return base[k] }); err != nil {
		t.Fatal(err)
	}
}

func TestReputationThresholdSampleFloorsAndRateBoundaries(t *testing.T) {
	for _, tc := range []struct {
		samples, bounces, complaints int64
		reason                       string
	}{
		{19, 10, 3, ""}, {20, 2, 1, ""}, {20, 3, 0, "bounce_rate"}, {60, 3, 0, "bounce_rate"}, {61, 3, 0, ""},
		{20, 0, 2, "complaint_rate"}, {2000, 0, 2, "complaint_rate"}, {2001, 0, 2, ""}, {20, 3, 2, "complaint_rate"},
	} {
		if got := (ReputationWindow{Samples: tc.samples, Bounces: tc.bounces, Complaints: tc.complaints}).reason(); got != tc.reason {
			t.Fatal(tc, got)
		}
	}
}

func TestReputationRoutesAreGatedAndOperatorOnly(t *testing.T) {
	var gate atomic.Bool
	s := &Service{Enabled: func() bool { return true }, OperationsEnabled: func() bool { return true }, OperationsToken: operatorToken, FeedbackEnabled: func() bool { return true }, FeedbackSources: map[string]FeedbackSource{"provider": feedbackSource()}, ReputationEnabled: gate.Load}
	h, err := Handler(s, testToken)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{ReputationPath, ReputationPath + "/holds", ReputationPath + "/events", ReputationPath + "/release"} {
		r := httptest.NewRequest(http.MethodPost, path, forbiddenRead{t})
		r.Header.Set("Authorization", "Bearer "+operatorToken)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 404 {
			t.Fatal("disabled reputation route visible", path, w.Code)
		}
	}
	gate.Store(true)
	for _, token := range []string{testToken, reportToken, feedbackToken, producerToken, ""} {
		r := httptest.NewRequest(http.MethodPost, ReputationPath+"/release", forbiddenRead{t})
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatal("non-operator could release", w.Code)
		}
	}
}

type reputationStatisticsFunc func(context.Context) (ReputationStatistics, error)

func (f reputationStatisticsFunc) reputationStatistics(ctx context.Context) (ReputationStatistics, error) {
	return f(ctx)
}

func TestReputationMonitorNoIOWhenOffAndClosedLabels(t *testing.T) {
	var gate, fail atomic.Bool
	calls := 0
	registry := prometheus.NewRegistry()
	registry.MustRegister(NewReputationMonitor(reputationStatisticsFunc(func(context.Context) (ReputationStatistics, error) {
		calls++
		if fail.Load() {
			return ReputationStatistics{}, errors.New("private provider diagnostic")
		}
		return ReputationStatistics{Holds: map[string]map[string]int64{"domain": {"bounce_rate": 2}}, Feedback: map[string]int64{"complained": 3}}, nil
	}), gate.Load))
	body := scrapeMonitor(t, registry)
	if calls != 0 || !strings.Contains(body, "maild_mail_reputation_enabled 0") || strings.Contains(body, "stats_up") {
		t.Fatal("disabled monitor read state")
	}
	gate.Store(true)
	body = scrapeMonitor(t, registry)
	if !strings.Contains(body, `maild_mail_reputation_holds{reason="bounce_rate",scope="domain"} 2`) || !strings.Contains(body, `maild_mail_reputation_feedback_recipients{kind="complained"} 3`) {
		t.Fatal(body)
	}
	fail.Store(true)
	body = scrapeMonitor(t, registry)
	if !strings.Contains(body, "maild_mail_reputation_stats_up 0") || strings.Contains(body, "private") || strings.Contains(body, "reputation_holds") {
		t.Fatal("failed read leaked stale statistics or diagnostics", body)
	}
}
