package internalmail

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestSuppressionControlsGateBeforeStoreCredentialsAndBody(t *testing.T) {
	for _, flag := range []string{"", "0", "true", " 1", "1 "} {
		if SuppressionControlsEnabled(func(key string) string {
			if key == EnableEnv {
				return "1"
			}
			return flag
		}) {
			t.Fatal("nonexact suppression flag enabled management")
		}
	}
	if SuppressionControlsEnabled(func(key string) string {
		if key != EnableEnv {
			t.Fatal("disabled master read configuration")
		}
		return "0"
	}) {
		t.Fatal("disabled master enabled management")
	}
	var enabled atomic.Bool
	s := &Service{Enabled: func() bool { return true }, OperationsEnabled: func() bool { return true }, OperationsToken: operatorToken, SuppressionControlsEnabled: enabled.Load}
	h, err := Handler(s, testToken)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{SuppressionControlsPath + "/status?address=one@example.net", SuppressionControlsPath + "/history?address=one@example.net", SuppressionControlsPath + "/add", SuppressionControlsPath + "/release"} {
		r := httptest.NewRequest(http.MethodPost, path, forbiddenRead{t})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 404 {
			t.Fatal("disabled suppression route visible", w.Code)
		}
	}
	if _, err := s.ChangeSuppression(t.Context(), "release", SuppressionRequest{}); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
	enabled.Store(true)
	for _, token := range []string{testToken, reportToken, feedbackToken, producerToken, ""} {
		r := httptest.NewRequest(http.MethodPost, SuppressionControlsPath+"/release", forbiddenRead{t})
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatal("non-operator read suppression input", w.Code)
		}
	}
	s.DeliveryEnabled = func() bool { return true }
	if err := s.DeliverOne(t.Context()); !errors.Is(err, ErrInvalid) {
		t.Fatal("worker accepted a store without admission fences", err)
	}
}

func TestSuppressionAddressesReviewAndHistoryBounds(t *testing.T) {
	for _, input := range []string{"one@example.net", "One@EXAMPLE.NET", "a+tag@example.net"} {
		address, err := NormalizeSuppressionAddress(input)
		if err != nil || address != strings.ToLower(input) {
			t.Fatal("valid bare address rejected", err)
		}
	}
	for _, input := range []string{"", "*", "*@example.net", "one?@example.net", "Display <one@example.net>", " one@example.net", "one@example.net\n", "one@example.net,two@example.net", "https://example.net", "one@localhost"} {
		if _, err := NormalizeSuppressionAddress(input); err == nil {
			t.Fatal("unsafe address accepted")
		}
	}
	revision := int64(0)
	input := SuppressionRequest{OperatorAuditInput: auditInput(), Address: "one@example.net", ExpectedRevision: &revision}
	if input.Validate("add") != nil || input.Validate("release") == nil {
		t.Fatal("review guard missing")
	}
	input.ConfirmedReviewed = true
	if input.Validate("release") != nil || input.Validate("add") == nil || input.Validate("unknown") == nil {
		t.Fatal("invalid action shape accepted")
	}
	input.ExpectedRevision = nil
	if input.Validate("release") == nil {
		t.Fatal("revision missing")
	}
	for _, after := range []string{"0", "-1", "01", "1.5", "9223372036854775808", "private"} {
		if _, err := suppressionHistoryList(OperatorList{Limit: 25, After: after}); err == nil {
			t.Fatal("invalid history cursor accepted")
		}
	}
	for _, raw := range []string{"address=one%40example.net&address=two%40example.net", "address=one%40example.net&limit=101", "address=one%40example.net&after=0", "address=%XX", "address=one%40example.net&status=accepted"} {
		r := httptest.NewRequest(http.MethodGet, SuppressionControlsPath+"/history", nil)
		r.URL.RawQuery = raw
		if _, _, err := suppressionQuery(r, true); err == nil {
			t.Fatal("invalid history query accepted")
		}
	}
}

type suppressionStatsFunc func(context.Context) (SuppressionControlStatistics, error)

func (f suppressionStatsFunc) suppressionControlStatistics(ctx context.Context) (SuppressionControlStatistics, error) {
	return f(ctx)
}

func TestSuppressionMetricsAreDefaultOffAndHaveNoAddressLabels(t *testing.T) {
	var enabled, failed atomic.Bool
	calls := 0
	registry := prometheus.NewRegistry()
	registry.MustRegister(NewSuppressionControlMonitor(suppressionStatsFunc(func(context.Context) (SuppressionControlStatistics, error) {
		calls++
		if failed.Load() {
			return SuppressionControlStatistics{}, errors.New("private database diagnostic")
		}
		return SuppressionControlStatistics{ManualBlocks: 1, BounceBlocks: 2, ComplaintBlocks: 3, ReleasedAddresses: 4}, nil
	}), enabled.Load))
	if text := scrapeMonitor(t, registry); calls != 0 || !strings.Contains(text, "suppression_controls_enabled 0") || strings.Contains(text, "manual_blocks") {
		t.Fatal("disabled metrics read state")
	}
	enabled.Store(true)
	text := scrapeMonitor(t, registry)
	for _, metric := range []string{"enabled 1", "stats_up 1", "manual_blocks 1", "bounce_blocks 2", "complaint_blocks 3", "released_addresses 4"} {
		if !strings.Contains(text, "maild_mail_suppression_controls_"+metric) {
			t.Fatal("missing aggregate", metric)
		}
	}
	metrics, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range metrics {
		if len(family.Metric) != 1 || len(family.Metric[0].Label) != 0 {
			t.Fatal("suppression identity leaked in metrics")
		}
	}
	failed.Store(true)
	text = scrapeMonitor(t, registry)
	if !strings.Contains(text, "suppression_controls_stats_up 0") || strings.Contains(text, "manual_blocks") || strings.Contains(text, "private") {
		t.Fatal("failed inventory claimed empty or exposed diagnostics")
	}
}
