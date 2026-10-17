package internalmail

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/expfmt"
)

type statisticsFunc func(context.Context) (Statistics, error)

func (f statisticsFunc) Statistics(ctx context.Context) (Statistics, error) { return f(ctx) }

func scrapeMonitor(t *testing.T, registry *prometheus.Registry) string {
	t.Helper()
	metrics, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	for _, metric := range metrics {
		if _, err := expfmt.MetricFamilyToText(&body, metric); err != nil {
			t.Fatal(err)
		}
	}
	return body.String()
}

func TestMonitorFlagsPrivacyAndFailureVisibility(t *testing.T) {
	var enabled, reports, fail atomic.Bool
	calls := 0
	monitor := NewMonitor(statisticsFunc(func(context.Context) (Statistics, error) {
		calls++
		if fail.Load() {
			return Statistics{}, errors.New("private database error")
		}
		return Statistics{Recipients: map[string]StateStat{"queued": {Count: 2, OldestSeconds: 300}, "submitted": {Count: 1, StateAgeSeconds: 500}, "private@example.net": {Count: 900}}, Events: map[string]int64{"accepted": 4, "private-diagnostic": 10}}, nil
	}), enabled.Load, enabled.Load, reports.Load, reports.Load)
	registry := prometheus.NewRegistry()
	registry.MustRegister(monitor)
	if body := scrapeMonitor(t, registry); !strings.Contains(body, "maild_mail_enabled 0") || calls != 0 {
		t.Fatal("disabled collection performed IO", body)
	}
	enabled.Store(true)
	body := scrapeMonitor(t, registry)
	if !strings.Contains(body, `maild_mail_recipients{state="queued"} 2`) || !strings.Contains(body, "maild_mail_oldest_pending_seconds 300") || strings.Contains(body, "private") {
		t.Fatal("incorrect or privacy-unsafe telemetry", body)
	}
	reports.Store(true)
	body = scrapeMonitor(t, registry)
	if !strings.Contains(body, "maild_mail_reports_enabled 1") || !strings.Contains(body, "maild_mail_feedback_enabled 1") || strings.Contains(body, "maild_mail_postfix_") {
		t.Fatal("API must not inspect privileged Postfix state", body)
	}
	fail.Store(true)
	body = scrapeMonitor(t, registry)
	if !strings.Contains(body, "maild_mail_stats_up 0") || strings.Contains(body, "maild_mail_recipients{") {
		t.Fatal("failed collection retained a healthy queue snapshot", body)
	}
}

func TestPGMonitoringSurvivesNewStoreAndExcludesOtherNamespaces(t *testing.T) {
	f := fixture(t)
	input := request()
	input.To = []string{"one@example.net"}
	m := submit(t, f, "durable-monitoring", input)
	f.delivery.Store(true)
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_recipients SET updated_at=now()-interval '10 minutes' WHERE id=$1", m.Recipients[0].ID); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewPGStore(f.pool, "platform")
	if err != nil {
		t.Fatal(err)
	}
	stats, err := restarted.Statistics(t.Context())
	if err != nil || stats.Recipients["submitted"].Count != 1 || stats.Recipients["submitted"].StateAgeSeconds < 600 {
		t.Fatal("lost submitted queue state after restart", stats, err)
	}
	other, err := NewPGStore(f.pool, "other_namespace")
	if err != nil {
		t.Fatal(err)
	}
	stats, err = other.Statistics(t.Context())
	if err != nil || len(stats.Recipients) != 0 || len(stats.Events) != 0 || stats.Suppressions != 0 {
		t.Fatal("telemetry escaped its namespace", stats, err)
	}
	if err := restarted.Report(t.Context(), Report{EventID: "monitor-bounce", RecipientID: m.Recipients[0].ID, Kind: "bounced", Code: "dsn_5.1.1"}); err != nil {
		t.Fatal(err)
	}
	stats, err = restarted.Statistics(t.Context())
	if err != nil || stats.Suppressions != 1 || stats.Outcomes["bounced"] != 1 || stats.Recipients["submitted"].Count != 0 {
		t.Fatal("missing durable bounce metrics", stats, err)
	}
}

func TestPostfixQueueInventoryCountsOnlyGregaleAndRejectsPartialData(t *testing.T) {
	now := time.Unix(1700001000, 0)
	input := fmt.Sprintf(`{"sender":"bounce+%s@example.test","arrival_time":1700000000,"recipients":[{"address":"private@example.net"}]}
{"sender":"MAILER-DAEMON","arrival_time":1700000500,"recipients":[{"address":"bounce+%s@example.test"}]}
{"sender":"unrelated@example.test","arrival_time":1700000000,"recipients":[{"address":"other@example.net"}]}`, dsnRecipient, dsnRecipient)
	queues, err := parsePostfixQueues(strings.NewReader(input), now)
	if err != nil || queues.Delivery.Count != 1 || queues.Delivery.OldestSeconds != 1000 || queues.Reports.Count != 1 || queues.Reports.OldestSeconds != 500 {
		t.Fatal(queues, err)
	}
	if queues.Total.Count != 3 || queues.Total.OldestSeconds != 1000 {
		t.Fatal("whole relay pressure excluded unrelated queued mail")
	}
	if _, err := parsePostfixQueues(strings.NewReader(`{"sender":"unrelated@example.test","arrival_time":1700010000,"recipients":[]}`), now); err == nil {
		t.Fatal("unreliable future arrival time appeared healthy")
	}
	if _, err := parsePostfixQueues(strings.NewReader(input+"\ninvalid"), now); err == nil {
		t.Fatal("accepted partial queue evidence")
	}
	if _, err := parsePostfixQueues(strings.NewReader(`{}`), now); err == nil {
		t.Fatal("missing inventory fields appeared healthy")
	}
}

func TestMonitoringExporterPublishesFailureAndGatesFileIO(t *testing.T) {
	var enabled atomic.Bool
	enabled.Store(true)
	monitor := NewMonitor(statisticsFunc(func(context.Context) (Statistics, error) { return Statistics{}, nil }), enabled.Load, enabled.Load, enabled.Load, enabled.Load)
	registry := prometheus.NewRegistry()
	registry.MustRegister(monitor)
	service := &Service{Enabled: enabled.Load, ReportsEnabled: enabled.Load, ReportToken: reportToken, Monitoring: promhttp.HandlerFor(registry, promhttp.HandlerOpts{})}
	handler, err := Handler(service, testToken)
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(t.TempDir(), "maild.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: time.Second}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	bridge, err := NewPostfixBridge(socket, reportToken, enabled.Load)
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "internal-mail.prom")
	bridge.queues = func(context.Context) (RelayQueues, error) {
		return RelayQueues{Reports: StateStat{Count: 3, OldestSeconds: 600}}, nil
	}
	if err := bridge.ExportMonitoring(t.Context(), output); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(output)
	if err != nil || !bytes.Contains(body, []byte("maild_mail_monitoring_up 1")) || !bytes.Contains(body, []byte("maild_mail_stats_up 1")) || !bytes.Contains(body, []byte(`maild_mail_postfix_queue_messages{kind="report"} 3`)) {
		t.Fatal(string(body), err)
	}
	_ = server.Close()
	if err := bridge.ExportMonitoring(t.Context(), output); err == nil {
		t.Fatal("unavailable API appeared healthy")
	}
	body, err = os.ReadFile(output)
	if err != nil || !bytes.Contains(body, []byte("maild_mail_monitoring_up 0")) || bytes.Contains(body, []byte("maild_mail_stats_up 1")) {
		t.Fatal("failed refresh retained healthy telemetry", string(body), err)
	}
	enabled.Store(false)
	if err := bridge.ExportMonitoring(t.Context(), output); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
	after, err := os.ReadFile(output)
	if err != nil || !bytes.Equal(body, after) {
		t.Fatal("disabled helper mutated the file", err)
	}
}
