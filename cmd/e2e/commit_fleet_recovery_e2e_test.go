package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/commitmanaged"
	"github.com/onebox-faas/faas/pkg/e2etest/postgresfixture"
)

const (
	commitFleetAffectedSources  = 32
	commitFleetHealthySources   = 4
	commitFleetControlInterval  = 2 * time.Second
	commitFleetOutage           = 6*time.Minute + 45*time.Second
	commitFleetRecoveryWindow   = 100 * time.Second
	commitFleetKnownSlowSources = 8
)

type commitFleetLatency struct {
	Samples int   `json:"samples"`
	P50     int64 `json:"p50_ms"`
	P95     int64 `json:"p95_ms"`
	Max     int64 `json:"max_ms"`
}

type commitFleetSourceTiming struct {
	Source       int   `json:"source"`
	Events       int   `json:"events"`
	FirstReceipt int64 `json:"restore_to_first_receipt_ms"`
	Drain        int64 `json:"restore_to_last_completion_ms"`
}

func runCommitFleetDrill(t *testing.T, f *normalPathFixture, consumer *pgxpool.Pool, primary commitConsumerLoadFixture, prometheusBinary string) {
	t.Helper()
	slowTLS := os.Getenv("GREGALE_COMMIT_FLEET_SLOW_TLS_DRILL") == "1"
	slowTLSCooldown := os.Getenv("GREGALE_COMMIT_FLEET_SLOW_TLS_COOLDOWN") == "1"
	tailTrace := os.Getenv("GREGALE_COMMIT_FLEET_TAIL_TRACE") == "1" || slowTLS
	fastConnect := os.Getenv("FAAS_COMMIT_RELAY_FAST_RESERVED_CONNECT") == "true"
	adaptiveConnect := os.Getenv("FAAS_COMMIT_RELAY_ADAPTIVE_RESERVED_CONNECT") == "true"
	knownSlowOutage := os.Getenv("GREGALE_COMMIT_FLEET_KNOWN_SLOW_OUTAGE") == "1"
	if slowTLSCooldown && (!slowTLS || !fastConnect) {
		t.Fatal("the slow TLS cooldown requires the slow TLS drill and the fast reserved connection budget")
	}
	if knownSlowOutage && (!slowTLS || !fastConnect) {
		t.Fatal("the known-slow outage requires the slow TLS drill and the fast reserved connection budget")
	}
	var knownSlowCount int
	if knownSlowOutage {
		knownSlowCount = commitFleetKnownSlowSources
	}
	if value := os.Getenv("GREGALE_COMMIT_FLEET_KNOWN_SLOW_SOURCES"); value != "" {
		count, err := strconv.Atoi(value)
		if !knownSlowOutage || err != nil || count < 1 || count >= commitFleetAffectedSources {
			t.Fatalf("GREGALE_COMMIT_FLEET_KNOWN_SLOW_SOURCES requires the known-slow outage and a count from 1 to %d", commitFleetAffectedSources-1)
		}
		knownSlowCount = count
	}
	var adaptiveMaxMS int64
	if adaptiveConnect {
		adaptiveMaxMS = commitmanaged.DefaultAdaptiveReservedConnectMax.Milliseconds()
		if value := os.Getenv("FAAS_COMMIT_RELAY_ADAPTIVE_RESERVED_CONNECT_MAX_MS"); value != "" {
			parsed, err := strconv.ParseInt(value, 10, 32)
			if err != nil {
				t.Fatalf("read configured adaptive connection cap: %v", err)
			}
			adaptiveMaxMS = parsed
		}
	}
	budget := 27 * time.Minute
	if tailTrace {
		budget = 37 * time.Minute
	}
	ctx, cancel := context.WithTimeout(t.Context(), budget)
	defer cancel()

	proxy := postgresfixture.OpenProxy(t, primary.customer, "127.0.0.1", 0)
	var knownSlowProxy *postgresfixture.Proxy
	if knownSlowCount > 0 {
		slow := postgresfixture.OpenProxy(t, primary.customer, "127.0.0.1", 0)
		slow.SetConnectDelay(1800 * time.Millisecond)
		knownSlowProxy = &slow
	}
	routeThroughProxy := func(source commitConsumerLoadFixture, port uint16) {
		t.Helper()
		sealed, err := primary.sealRelay(source.source.ID, source.customer.Config().ConnConfig.Database, port)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.store.SetCommitSourceConnection(ctx, source.source.AccountID, source.source.ID, sealed); err != nil {
			t.Fatal(err)
		}
	}
	routeThroughProxy(primary, proxy.Port)
	affected := []commitConsumerLoadFixture{primary}
	for i := 1; i < commitFleetAffectedSources; i++ {
		port := proxy.Port
		if knownSlowProxy != nil && i >= commitFleetAffectedSources-knownSlowCount {
			port = knownSlowProxy.Port
		}
		affected = append(affected, addCommitProfileSource(t, ctx, f.store, primary, fmt.Sprintf("affected_%02d", i+1), port))
	}
	var controls []commitConsumerLoadFixture
	for i := range commitFleetHealthySources {
		controls = append(controls, addCommitProfileSource(t, ctx, f.store, primary, fmt.Sprintf("healthy_%02d", i+1), 0))
	}
	all := append(slices.Clone(affected), controls...)
	recordCommitProfileClaims(t, ctx, f.h.Pool)
	if tailTrace {
		recordCommitRelayPolls(t, ctx, f.h.Pool, primary.customer)
	}
	expectedEvents := 2 // Producer handoff and poison repair.
	var slowControls []commitFleetTLSControl
	if slowTLS {
		for i, delay := range []time.Duration{0, 500 * time.Millisecond, 1250 * time.Millisecond, 0} {
			controlProxy := postgresfixture.OpenProxy(t, controls[i].customer, "127.0.0.1", 0)
			controlProxy.SetConnectDelay(delay)
			routeThroughProxy(controls[i], controlProxy.Port)
			slowControls = append(slowControls, commitFleetTLSControl{source: controls[i], proxy: controlProxy, delay: delay})
		}
		if fastConnect && !knownSlowOutage && !adaptiveConnect {
			verifyCommitSlowTLSRestart(t, ctx, f, consumer, all, slowControls[2].source, expectedEvents+1)
			expectedEvents++
		}
		if slowTLSCooldown {
			recordCommitSlowTLSCooldown(t, ctx, f.h.Pool)
		}
	}

	peerMetrics := f.h.StartScheddPeer(t)
	prometheus := startCommitPrometheus(t, ctx, prometheusBinary, f.h.ScheddMetricsURL, peerMetrics)
	known := commitFleetObservation(len(all), 0)
	waitCommitPrometheus(t, ctx, prometheus, time.Minute, "inactive", known)
	baselineStart := time.Now()
	baseline := awaitCommitProfileProducer(t, ctx, startCommitProfileProducer(ctx, controls, 0, 30, commitFleetControlInterval))
	expectedEvents += len(baseline)
	waitCommitProfileDrain(t, ctx, f, consumer, all, expectedEvents)
	baselineLatency := commitFleetLatencies(readCommitProfileTimings(t, ctx, f.h.Pool, consumer, controls, baseline, baselineStart))
	logCommitFleetReport(t, struct {
		Baseline      commitFleetLatency `json:"healthy_baseline"`
		Reserved      bool               `json:"reserved_healthy_worker"`
		FastProbe     bool               `json:"fast_reserved_connect"`
		Adaptive      bool               `json:"adaptive_reserved_connect"`
		AdaptiveMaxMS int64              `json:"adaptive_reserved_connect_max_ms,omitempty"`
		KnownSlow     int                `json:"known_slow_affected_sources,omitempty"`
	}{Baseline: baselineLatency, Reserved: os.Getenv("FAAS_COMMIT_RELAY_RESERVE_HEALTHY_WORKER") == "true",
		FastProbe: fastConnect, Adaptive: adaptiveConnect, AdaptiveMaxMS: adaptiveMaxMS, KnownSlow: knownSlowCount})
	if knownSlowOutage && adaptiveConnect {
		var ids []string
		for _, source := range affected[len(affected)-knownSlowCount:] {
			ids = append(ids, source.source.ID)
		}
		waitCommitLoadCondition(t, ctx, "recent successful connection observations for the known-slow sources", func() (bool, error) {
			var learned int
			err := f.h.Pool.QueryRow(ctx, `SELECT count(*) FROM commit_sources
				WHERE id::text=ANY($1) AND relay_status='healthy' AND relay_connect_ms>=1800 AND relay_connect_observed_at=last_checked_at`,
				ids).Scan(&learned)
			return learned == len(ids), err
		})
	}

	faults := []string{"immediate", "timeout"}
	if os.Getenv("GREGALE_COMMIT_FLEET_RESERVATION_DRILL") == "1" {
		faults = []string{"timeout", "all-timeout"}
	}
	if tailTrace {
		faults = []string{"timeout-early", "timeout-late", "all-timeout"}
	}
	if slowTLS {
		faults = []string{"timeout-early"}
	}
	for _, fault := range faults {
		faulted, healthySources := affected, controls
		var anchor commitFleetPollAnchor
		if fault == "all-timeout" {
			for _, control := range controls {
				routeThroughProxy(control, proxy.Port)
			}
			waitCommitPrometheus(t, ctx, prometheus, time.Minute, "inactive", known)
			faulted, healthySources = all, nil
		} else if tailTrace {
			anchor = waitCommitFleetFaultStart(t, ctx, f.h.Pool, controls[0].source.ID, fault)
		}
		proxy.StartConnectionTrace()
		if knownSlowProxy != nil {
			knownSlowProxy.StartConnectionTrace()
		}
		for _, control := range slowControls {
			control.proxy.StartConnectionTrace()
		}
		startedAt := time.Now()
		if fault == "immediate" {
			proxy.SetAvailable(false)
		} else {
			// The production relay allows five seconds for connect and TLS.
			proxy.SetConnectDelay(7 * time.Second)
			if knownSlowProxy != nil {
				knownSlowProxy.SetConnectDelay(7 * time.Second)
			}
		}
		var healthy <-chan commitProfileProduction
		if len(healthySources) > 0 {
			healthy = startCommitProfileProducer(ctx, healthySources, 0, int((commitFleetOutage+commitFleetRecoveryWindow)/commitFleetControlInterval), commitFleetControlInterval)
		}
		outage := startCommitProfileProducer(ctx, faulted, 0, 2*len(faulted), commitProfileEventInterval)
		t.Logf("Commit fleet drill started %s fault", fault)
		unavailable := commitFleetObservation(len(all), len(faulted)) + `
			and on() (count(sum by(instance) (increase(schedd_commit_relay_cycles_total{result="database_unavailable"}[4m])) > 0) == 2)`
		waitCommitPrometheus(t, ctx, prometheus, 7*time.Minute, "firing", unavailable)
		firedAt := time.Now()
		events := awaitCommitProfileProducer(t, ctx, outage)
		waitCommitRelayTraceUntil(t, ctx, startedAt.Add(commitFleetOutage))
		restoredAt := time.Now()
		proxy.SetAvailable(true)
		proxy.SetConnectDelay(0)
		if knownSlowProxy != nil {
			knownSlowProxy.SetConnectDelay(1800 * time.Millisecond)
		}
		t.Logf("Commit fleet drill restored %s fault with %d affected events", fault, len(events))
		waitCommitPrometheus(t, ctx, prometheus, 3*time.Minute, "inactive", known)
		clearedAt := time.Now()
		var controlEvents []commitProfileEvent
		if healthy != nil {
			controlEvents = awaitCommitProfileProducer(t, ctx, healthy)
		}
		recoveryEnd := restoredAt.Add(commitFleetRecoveryWindow)
		waitCommitRelayTraceUntil(t, ctx, recoveryEnd)
		expectedEvents += len(events) + len(controlEvents)
		waitCommitProfileDrain(t, ctx, f, consumer, all, expectedEvents)
		waitCommitPrometheus(t, ctx, prometheus, time.Minute, "inactive", known+`
			and on() (count(schedd_commit_relay_pending_events == 0) == 2)
			and on() (count(schedd_commit_relay_blocked_events == 0) == 2)`)

		recovered := readCommitProfileTimings(t, ctx, f.h.Pool, consumer, faulted, events, restoredAt)
		controlTimings := readCommitProfileTimings(t, ctx, f.h.Pool, consumer, healthySources, controlEvents, startedAt)
		if tailTrace && len(healthySources) > 0 {
			reportCommitFleetTail(t, ctx, f.h.Pool, primary.relayLog, fault, anchor, faulted, healthySources, controlEvents, controlTimings, startedAt, restoredAt)
		}
		if slowTLS {
			reportCommitFleetSlowTLS(t, ctx, f.h.Pool, slowControls, controlEvents, controlTimings, startedAt, restoredAt, fastConnect, slowTLSCooldown, adaptiveConnect)
		}
		var during, after []commitProfileTiming
		for _, event := range controlTimings {
			if event.producedAt.Before(restoredAt) {
				during = append(during, event)
			} else {
				after = append(after, event)
			}
		}
		perSource := make([]commitFleetSourceTiming, len(faulted))
		for _, event := range recovered {
			source := &perSource[event.Source-1]
			if source.Events == 0 {
				source.Source = event.Source
				source.FirstReceipt = event.ReceiptMS
			}
			source.Events++
			source.Drain = max(source.Drain, event.CompletedMS)
		}
		for _, source := range perSource {
			if source.Events != 2 {
				t.Fatalf("expected two completions at affected source %d, got %d", source.Source, source.Events)
			}
		}
		attempts := proxy.ConnectionAttempts()
		if knownSlowProxy != nil {
			attempts = append(attempts, knownSlowProxy.ConnectionAttempts()...)
			slices.SortFunc(attempts, time.Time.Compare)
		}
		outageAttempts, outagePeak := commitFleetAttemptRate(attempts, startedAt, restoredAt)
		recoveryAttempts, recoveryPeak := commitFleetAttemptRate(attempts, restoredAt, recoveryEnd)
		if outageAttempts < len(faulted) || (healthy != nil && (len(during) == 0 || len(after) == 0)) {
			t.Fatal("fleet drill did not capture enough connection attempts and control receipts")
		}
		logCommitFleetReport(t, struct {
			Fault                string                    `json:"fault"`
			OutageMS             int64                     `json:"outage_ms"`
			AlertsFiringMS       int64                     `json:"outage_to_alerts_firing_ms"`
			AlertsClearMS        int64                     `json:"restore_to_alerts_clear_ms"`
			OutageAttempts       int                       `json:"outage_tcp_attempts"`
			OutagePeak           int                       `json:"outage_peak_attempts_per_second"`
			RecoveryAttempts     int                       `json:"recovery_tcp_attempts"`
			RecoveryPeak         int                       `json:"recovery_peak_attempts_per_second"`
			HealthyDuring        *commitFleetLatency       `json:"healthy_during_outage,omitempty"`
			HealthyAfter         *commitFleetLatency       `json:"healthy_after_restore,omitempty"`
			RecoveredSourceTimes []commitFleetSourceTiming `json:"recovered_sources"`
		}{
			Fault: fault, OutageMS: restoredAt.Sub(startedAt).Milliseconds(),
			AlertsFiringMS: firedAt.Sub(startedAt).Milliseconds(), AlertsClearMS: clearedAt.Sub(restoredAt).Milliseconds(),
			OutageAttempts: outageAttempts, OutagePeak: outagePeak, RecoveryAttempts: recoveryAttempts, RecoveryPeak: recoveryPeak,
			HealthyDuring: commitFleetLatencyIfObserved(during), HealthyAfter: commitFleetLatencyIfObserved(after), RecoveredSourceTimes: perSource,
		})
	}
}

func commitFleetLatencyIfObserved(events []commitProfileTiming) *commitFleetLatency {
	if len(events) == 0 {
		return nil
	}
	result := commitFleetLatencies(events)
	return &result
}

func commitFleetObservation(enabled, unavailable int) string {
	return commitPrometheusObservation(enabled, unavailable) + fmt.Sprintf(`
		and on() (count(up{job="commit-schedd"} == 1) == 2)
		and on() (count(schedd_commit_relay_unknown_sources == %d) == 2)
		and on() (count(schedd_commit_relay_failing_sources == %d) == 2)`, unavailable, unavailable)
}

func commitFleetLatencies(events []commitProfileTiming) commitFleetLatency {
	values := make([]int64, len(events))
	for i, event := range events {
		values[i] = event.receiptAt.Sub(event.producedAt).Milliseconds()
	}
	slices.Sort(values)
	return commitFleetLatency{Samples: len(values), P50: values[(50*len(values)-1)/100],
		P95: values[(95*len(values)-1)/100], Max: values[len(values)-1]}
}

func commitFleetAttemptRate(attempts []time.Time, start, end time.Time) (count, peak int) {
	first := 0
	for first < len(attempts) && attempts[first].Before(start) {
		first++
	}
	for i := first; i < len(attempts) && attempts[i].Before(end); i++ {
		for attempts[i].Sub(attempts[first]) >= time.Second {
			first++
		}
		count++
		peak = max(peak, i-first+1)
	}
	return count, peak
}

func logCommitFleetReport(t *testing.T, report any) {
	t.Helper()
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Commit fleet drill result %s", data)
}
