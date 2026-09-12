package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestHostJournalMetricsSample(t *testing.T) {
	reg := prometheus.NewRegistry()
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		switch {
		case name == "journalctl":
			joined := strings.Join(args, " ")
			for _, want := range []string{
				"--unit=networkd-dispatcher.service",
				"--since=-5min",
				"--grep=^(WARNING|ERROR|CRITICAL):",
			} {
				if !strings.Contains(joined, want) {
					t.Errorf("journalctl args %q missing %q", joined, want)
				}
			}
			return []byte("persistent link failed\n\nnetworkctl list failed\n"), nil
		case name == "du" && args[len(args)-1] == "/var/log/journal":
			return []byte("1048576\t/var/log/journal\n"), nil
		case name == "du" && args[len(args)-1] == "/run/log/journal":
			return []byte("2048\t/run/log/journal\n"), nil
		default:
			return nil, errors.New("unexpected command")
		}
	}
	m := newHostJournalMetrics(reg, run, func() time.Time { return time.Unix(1_725_000_000, 0) })
	if err := m.sample(context.Background()); err != nil {
		t.Fatal(err)
	}

	for name, tc := range map[string]struct {
		collector prometheus.Collector
		want      float64
	}{
		"vmmd_networkd_dispatcher_errors_last_5m": {m.dispatcherErrors, 2},
		"vmmd_journal_disk_usage_bytes":           {m.journalBytes, 1_050_624},
		"vmmd_host_journal_metrics_up":            {m.up, 1},
		"vmmd_host_journal_metrics_last_success_timestamp_seconds": {
			m.lastSuccess, 1_725_000_000,
		},
	} {
		if got := testutil.ToFloat64(tc.collector); got != tc.want {
			t.Errorf("%s = %v, want %v", name, got, tc.want)
		}
	}
}

func TestHostJournalMetricsReachCanonicalVMMDMetricsEndpoint(t *testing.T) {
	ops := wire.NewOpsMetrics("vmmd")
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "journalctl" {
			return []byte("persistent failure\n"), nil
		}
		if name == "du" && args[len(args)-1] == "/var/log/journal" {
			return []byte("4096\t/var/log/journal\n"), nil
		}
		return nil, errors.New("directory absent")
	}
	hostMetrics := newHostJournalMetrics(ops.Registry(), run, time.Now)
	if err := hostMetrics.sample(context.Background()); err != nil {
		t.Fatal(err)
	}

	mux := newMetricsMux(
		ops,
		fcvm.NewColdBootMetrics(),
		fcvm.NewFrameworkReadyMetrics(),
		fcvm.NewWakePhaseMetrics(),
		fcvm.NewDiskMetrics(),
	)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, metricsPath, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: status %d: %s", metricsPath, rec.Code, rec.Body.String())
	}
	for _, want := range []string{
		"vmmd_networkd_dispatcher_errors_last_5m 1",
		"vmmd_journal_disk_usage_bytes 4096",
		"vmmd_host_journal_metrics_up 1",
	} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("canonical metrics endpoint missing %q", want)
		}
	}
}

func TestHostJournalMetricsPartialFailureKeepsLastGoodValues(t *testing.T) {
	reg := prometheus.NewRegistry()
	failJournal := false
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "journalctl" {
			if failJournal {
				return nil, errors.New("journal unavailable")
			}
			return []byte("one error\n"), nil
		}
		if name == "du" && args[len(args)-1] == "/var/log/journal" {
			return []byte("512\t/var/log/journal\n"), nil
		}
		return nil, errors.New("directory absent")
	}
	m := newHostJournalMetrics(reg, run, time.Now)
	if err := m.sample(context.Background()); err != nil {
		t.Fatal(err)
	}
	failJournal = true
	if err := m.sample(context.Background()); err == nil {
		t.Fatal("sample unexpectedly succeeded")
	}

	if got := testutil.ToFloat64(m.up); got != 0 {
		t.Errorf("up = %v, want 0", got)
	}
	if got := testutil.ToFloat64(m.dispatcherErrors); got != 1 {
		t.Errorf("dispatcher errors = %v, want last good value 1", got)
	}
	if got := testutil.ToFloat64(m.journalBytes); got != 512 {
		t.Errorf("journal bytes = %v, want 512", got)
	}
}

func TestNetworkdDispatcherFilterIsNarrow(t *testing.T) {
	patterns := dispatcherFilterPatterns(t)
	for _, message := range []string{
		"WARNING:Unknown index 812 seen, reloading interface list",
		"ERROR:Unknown interface index 812 seen even after reload",
		`ERROR:Failed to get interface "vh42" status: command disappeared`,
	} {
		if !matchesAny(patterns, message) {
			t.Errorf("expected ephemeral message to be filtered: %q", message)
		}
	}
	for _, message := range []string{
		`ERROR:Failed to get interface "ens4" status: persistent link failed`,
		"ERROR:networkctl list failed: exit status 1",
		"WARNING:Exit status 1 from script /etc/networkd-dispatcher/routable.d/50-route",
		`ERROR:Failed to get interface "vh-production" status: malformed managed name`,
	} {
		if matchesAny(patterns, message) {
			t.Errorf("persistent/actionable message was filtered: %q", message)
		}
	}
}

func TestJournalPolicyHasFiniteBounds(t *testing.T) {
	defaults, err := os.ReadFile(filepath.Join("..", "..", "deploy", "ansible", "roles", "host_hardening", "defaults", "main.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"faas_hardening_journal_system_max_use: 512M",
		"faas_hardening_journal_runtime_max_use: 128M",
		"faas_hardening_journal_max_retention: 7day",
	} {
		if !strings.Contains(string(defaults), want) {
			t.Errorf("journal defaults missing %q", want)
		}
	}
	template, err := os.ReadFile(filepath.Join("..", "..", "deploy", "ansible", "roles", "host_hardening", "templates", "60-faas-journal-bounds.conf.j2"))
	if err != nil {
		t.Fatal(err)
	}
	for _, directive := range []string{
		"SystemMaxUse=",
		"SystemKeepFree=",
		"RuntimeMaxUse=",
		"RuntimeKeepFree=",
		"MaxRetentionSec=",
	} {
		if !strings.Contains(string(template), directive) {
			t.Errorf("journal template missing %q", directive)
		}
	}
}

// The implementation reads the deployed systemd template so this unit test
// pins the operational boundary, including persistent-link visibility.
func dispatcherFilterPatterns(t *testing.T) []*regexp.Regexp {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "deploy", "ansible", "roles", "vmmd_service", "templates", "60-faas-firecracker-links.conf.j2"))
	if err != nil {
		t.Fatal(err)
	}
	var patterns []*regexp.Regexp
	for _, line := range strings.Split(string(raw), "\n") {
		const prefix = "LogFilterPatterns='~"
		if !strings.HasPrefix(line, prefix) || !strings.HasSuffix(line, "'") {
			continue
		}
		pattern, err := regexp.Compile(strings.TrimSuffix(strings.TrimPrefix(line, prefix), "'"))
		if err != nil {
			t.Fatalf("compile %q: %v", line, err)
		}
		patterns = append(patterns, pattern)
	}
	if len(patterns) != 3 {
		t.Fatalf("got %d deny patterns, want 3", len(patterns))
	}
	return patterns
}

func matchesAny(patterns []*regexp.Regexp, message string) bool {
	for _, pattern := range patterns {
		if pattern.MatchString(message) {
			return true
		}
	}
	return false
}
