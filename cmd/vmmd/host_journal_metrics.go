package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	hostJournalSampleInterval = time.Minute
	hostJournalSampleTimeout  = 10 * time.Second
)

var hostJournalDirs = []string{"/var/log/journal", "/run/log/journal"}

type hostJournalCommand func(context.Context, string, ...string) ([]byte, error)

// hostJournalMetrics samples the two host-only signals behind issue #2350.
// vmmd owns this collector because it already runs once per compute host as
// root and its private /metrics endpoint is discovered by the control-plane
// Prometheus. Keeping command execution on a ticker means Prometheus scrapes
// never block on journalctl or filesystem traversal.
type hostJournalMetrics struct {
	run              hostJournalCommand
	now              func() time.Time
	dispatcherErrors prometheus.Gauge
	journalBytes     prometheus.Gauge
	up               prometheus.Gauge
	lastSuccess      prometheus.Gauge
}

func newHostJournalMetrics(reg prometheus.Registerer, run hostJournalCommand, now func() time.Time) *hostJournalMetrics {
	if run == nil {
		run = execHostJournalCommand
	}
	if now == nil {
		now = time.Now
	}
	m := &hostJournalMetrics{
		run: run,
		now: now,
		dispatcherErrors: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "vmmd_networkd_dispatcher_errors_last_5m",
			Help: "Warning-or-higher networkd-dispatcher journal entries on this compute host during the last five minutes. Expected stale Firecracker link races are filtered before journald; any positive value is actionable.",
		}),
		journalBytes: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "vmmd_journal_disk_usage_bytes",
			Help: "Bytes occupied by persistent and runtime systemd journals on this compute host.",
		}),
		up: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "vmmd_host_journal_metrics_up",
			Help: "Whether vmmd successfully sampled networkd-dispatcher errors and local journal disk usage during its latest host-journal collection pass (1 success, 0 failure).",
		}),
		lastSuccess: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "vmmd_host_journal_metrics_last_success_timestamp_seconds",
			Help: "Unix timestamp of vmmd's latest successful networkd-dispatcher and journal-usage collection pass.",
		}),
	}
	reg.MustRegister(m.dispatcherErrors, m.journalBytes, m.up, m.lastSuccess)
	return m
}

func execHostJournalCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// sample leaves the last good values in place on a partial failure and marks
// the collector down. This prevents a transient journalctl/du failure from
// being reported as zero errors or zero bytes.
func (m *hostJournalMetrics) sample(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, hostJournalSampleTimeout)
	defer cancel()

	journalOutput, journalErr := m.run(ctx, "journalctl",
		"--unit=networkd-dispatcher.service",
		"--since=-5min",
		"--grep=^(WARNING|ERROR|CRITICAL):",
		"--no-pager",
		"--quiet",
		"--output=cat",
	)
	if journalErr == nil {
		m.dispatcherErrors.Set(float64(nonEmptyLineCount(journalOutput)))
	}

	var (
		journalBytes uint64
		usageOK      bool
		usageErrs    []error
	)
	for _, dir := range hostJournalDirs {
		out, err := m.run(ctx, "du", "-sb", "--", dir)
		if err != nil {
			usageErrs = append(usageErrs, fmt.Errorf("du %s: %w", dir, err))
			continue
		}
		fields := strings.Fields(string(out))
		if len(fields) == 0 {
			usageErrs = append(usageErrs, fmt.Errorf("du %s: empty output", dir))
			continue
		}
		value, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			usageErrs = append(usageErrs, fmt.Errorf("du %s: parse bytes %q: %w", dir, fields[0], err))
			continue
		}
		journalBytes += value
		usageOK = true
	}
	if usageOK {
		m.journalBytes.Set(float64(journalBytes))
	}

	// A host commonly has only persistent or only runtime storage. Missing one
	// directory is harmless when the other produced a valid usage value.
	if journalErr == nil && usageOK {
		m.up.Set(1)
		m.lastSuccess.Set(float64(m.now().Unix()))
		return nil
	}
	m.up.Set(0)
	if journalErr != nil {
		usageErrs = append(usageErrs, fmt.Errorf("journalctl: %w", journalErr))
	}
	return errors.Join(usageErrs...)
}

func nonEmptyLineCount(out []byte) int {
	count := 0
	for _, line := range bytes.Split(out, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) > 0 {
			count++
		}
	}
	return count
}

func (m *hostJournalMetrics) runSampler(ctx context.Context, log *slog.Logger) {
	sample := func() {
		if err := m.sample(ctx); err != nil && ctx.Err() == nil {
			log.Warn("vmmd: host journal metric sample failed", "err", err)
		}
	}
	sample()
	ticker := time.NewTicker(hostJournalSampleInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sample()
		}
	}
}
