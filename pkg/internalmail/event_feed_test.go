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

func TestEventFeedDefaultOffAndProducerPrerequisite(t *testing.T) {
	for _, tc := range []struct{ master, flag string }{{"", "1"}, {"true", "1"}, {"1", ""}, {"1", "0"}, {"1", "true"}, {"1", "1 "}} {
		if EventFeedEnabled(func(k string) string {
			if k == EnableEnv {
				return tc.master
			}
			return tc.flag
		}) {
			t.Fatal("event feed enabled without exact opt-in")
		}
	}
	s := &Service{Enabled: func() bool { return true }}
	if _, err := s.PublishEventFeed(t.Context()); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
	if _, err := s.PullEventFeed(t.Context(), "bad", "bad", 0); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
	if _, err := s.AckEventFeed(t.Context(), "bad", "bad", EventFeedAckInput{}); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
	if _, err := s.ReplayEventConsumer(t.Context(), EventFeedReplayRequest{}); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
	base := map[string]string{EnableEnv: "1", EventFeedEnableEnv: "1", "FAAS_INTERNAL_MAIL_TOKEN": testToken, "FAAS_INTERNAL_MAIL_AGE_IDENTITY_PATH": "/private/mail.age", "FAAS_INTERNAL_MAIL_SENDING_IP": "192.0.2.10"}
	if _, err := LoadConfig(func(k string) string {
		if k == "FAAS_INTERNAL_MAIL_PRODUCERS" {
			t.Fatal("missing prerequisite read enrollment")
		}
		return base[k]
	}); err == nil {
		t.Fatal("feed started without producers")
	}
	producers, _ := json.Marshal([]Producer{testProducer("auth", producerToken)})
	base[ProducersEnableEnv], base["FAAS_INTERNAL_MAIL_PRODUCERS"] = "1", string(producers)
	if _, err := LoadConfig(func(k string) string { return base[k] }); err != nil {
		t.Fatal(err)
	}
	s.ProducersEnabled = func() bool { return true }
	s.EventFeedEnabled = func() bool { return true }
	if _, err := s.PublishEventFeed(t.Context()); !errors.Is(err, ErrInvalid) {
		t.Fatal("unsupported store published", err)
	}
	for _, name := range []string{"", "../secret", "UPPER", "name?x=y", strings.Repeat("a", 33)} {
		if _, err := EventConsumerRoute("auth", name); err == nil {
			t.Fatal("unsafe name accepted", name)
		}
	}
}

func TestEventFeedRoutesGateBeforeCredentialsAndInput(t *testing.T) {
	var enabled atomic.Bool
	s := &Service{Enabled: func() bool { return true }, ProducersEnabled: func() bool { return true }, Producers: map[string]Producer{"auth": testProducer("auth", producerToken)}, OperationsEnabled: func() bool { return true }, OperationsToken: operatorToken, EventFeedEnabled: enabled.Load}
	h, err := Handler(s, testToken)
	if err != nil {
		t.Fatal(err)
	}
	base, _ := EventConsumerRoute("auth", "effects")
	for _, route := range []string{base, base + "/pull", base + "/ack", EventFeedOperationsPath + "/auth", EventFeedOperationsPath + "/replay"} {
		r := httptest.NewRequest(http.MethodPost, route, forbiddenRead{t})
		r.Header.Set("Authorization", "Bearer "+producerToken)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 404 {
			t.Fatal("disabled route visible", route, w.Code)
		}
	}
	enabled.Store(true)
	for _, token := range []string{testToken, reportToken, feedbackToken, operatorToken, ""} {
		r := httptest.NewRequest(http.MethodPost, base+"/pull", forbiddenRead{t})
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatal("non-producer credential could pull", w.Code)
		}
	}
	for _, token := range []string{testToken, producerToken, reportToken, feedbackToken, ""} {
		r := httptest.NewRequest(http.MethodPost, EventFeedOperationsPath+"/replay", forbiddenRead{t})
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatal("non-operator credential could rewind", w.Code)
		}
	}
}

type eventFeedStatsFunc func(context.Context) (EventFeedStatistics, error)

func (f eventFeedStatsFunc) eventFeedStatistics(ctx context.Context) (EventFeedStatistics, error) {
	return f(ctx)
}

func TestEventFeedMonitorDefaultOffFailureAndNoIdentityLabels(t *testing.T) {
	var enabled, failed atomic.Bool
	calls := 0
	registry := prometheus.NewRegistry()
	registry.MustRegister(NewEventFeedMonitor(eventFeedStatsFunc(func(context.Context) (EventFeedStatistics, error) {
		calls++
		if failed.Load() {
			return EventFeedStatistics{}, errors.New("private consumer diagnostic")
		}
		return EventFeedStatistics{Unpublished: 2, Published: 10, Consumers: 2, PendingBatches: 1, Unacked: 7, MaxBacklog: 5, OldestSeconds: 30}, nil
	}), enabled.Load))
	body := scrapeMonitor(t, registry)
	if calls != 0 || !strings.Contains(body, "maild_mail_event_feed_enabled 0") || strings.Contains(body, "stats_up") {
		t.Fatal("disabled monitor read state")
	}
	enabled.Store(true)
	body = scrapeMonitor(t, registry)
	for _, sample := range []string{"maild_mail_event_feed_unacked_events 7", "maild_mail_event_feed_max_consumer_backlog 5", "maild_mail_event_feed_oldest_unacked_seconds 30"} {
		if !strings.Contains(body, sample) {
			t.Fatal(body)
		}
	}
	if strings.Contains(body, "{") {
		t.Fatal("feed metric has identity labels", body)
	}
	failed.Store(true)
	body = scrapeMonitor(t, registry)
	if !strings.Contains(body, "maild_mail_event_feed_stats_up 0") || strings.Contains(body, "private") || strings.Contains(body, "unacked_events") {
		t.Fatal("failed collection exposed stale state or diagnostics")
	}
}
