package main

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"

	"filippo.io/age"
	commitwork "github.com/onebox-faas/faas/pkg/commit"
	"github.com/onebox-faas/faas/pkg/commitmanaged"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
)

func startCommitRelay(ctx context.Context, store *state.PgStore, identities []*age.X25519Identity, log *slog.Logger, registry prometheus.Registerer) error {
	enabled := os.Getenv("FAAS_COMMIT_RELAY_ENABLED")
	if enabled == "" || enabled == "false" {
		return nil
	}
	if enabled != "true" {
		return errors.New("commit: FAAS_COMMIT_RELAY_ENABLED must be true or false")
	}
	if len(identities) == 0 {
		return errors.New("commit: managed relay requires host age identities")
	}
	// ADR-20261112140000 §6: changes sources may reach any public customer
	// database. The allowlist below may then be empty; it still governs every
	// other Commit source.
	public := os.Getenv("FAAS_COMMIT_PUBLIC_DATABASES")
	if public != "" && public != "true" && public != "false" {
		return errors.New("commit: FAAS_COMMIT_PUBLIC_DATABASES must be true or false")
	}
	publicDatabases := public == "true"
	reserveHealthy := os.Getenv("FAAS_COMMIT_RELAY_RESERVE_HEALTHY_WORKER")
	if reserveHealthy != "" && reserveHealthy != "true" && reserveHealthy != "false" {
		return errors.New("commit: FAAS_COMMIT_RELAY_RESERVE_HEALTHY_WORKER must be true or false")
	}
	fastReservedConnect := os.Getenv("FAAS_COMMIT_RELAY_FAST_RESERVED_CONNECT")
	if fastReservedConnect != "" && fastReservedConnect != "true" && fastReservedConnect != "false" {
		return errors.New("commit: FAAS_COMMIT_RELAY_FAST_RESERVED_CONNECT must be true or false")
	}
	if fastReservedConnect == "true" && reserveHealthy != "true" {
		return errors.New("commit: fast reserved connection setup requires the reserved healthy worker")
	}
	adaptiveReservedConnect := os.Getenv("FAAS_COMMIT_RELAY_ADAPTIVE_RESERVED_CONNECT")
	if adaptiveReservedConnect != "" && adaptiveReservedConnect != "true" && adaptiveReservedConnect != "false" {
		return errors.New("commit: FAAS_COMMIT_RELAY_ADAPTIVE_RESERVED_CONNECT must be true or false")
	}
	if adaptiveReservedConnect == "true" && fastReservedConnect != "true" {
		return errors.New("commit: adaptive reserved connection setup requires the fast reserved connection budget")
	}
	var adaptiveReservedConnectMax time.Duration
	if adaptiveReservedConnect == "true" {
		adaptiveReservedConnectMax = commitmanaged.DefaultAdaptiveReservedConnectMax
	}
	if value := os.Getenv("FAAS_COMMIT_RELAY_ADAPTIVE_RESERVED_CONNECT_MAX_MS"); value != "" {
		if adaptiveReservedConnectMax == 0 {
			return errors.New("commit: a reserved connection cap requires adaptive reserved connection setup")
		}
		ms, err := strconv.ParseInt(value, 10, 32)
		if err != nil || ms < commitmanaged.FastReservedConnectBudget.Milliseconds() || ms > commitmanaged.DefaultAdaptiveReservedConnectMax.Milliseconds() {
			return errors.New("commit: FAAS_COMMIT_RELAY_ADAPTIVE_RESERVED_CONNECT_MAX_MS must be an integer from 750 to 2000")
		}
		adaptiveReservedConnectMax = time.Duration(ms) * time.Millisecond
	}
	policy := commitwork.NetworkPolicy{Hosts: map[string]bool{}}
	hosts, cidrs := os.Getenv("FAAS_COMMIT_DATABASE_HOSTS"), os.Getenv("FAAS_COMMIT_DATABASE_CIDRS")
	if !publicDatabases || hosts != "" || cidrs != "" {
		for _, host := range strings.Split(hosts, ",") {
			host = strings.TrimSpace(host)
			if host == "" || strings.ContainsAny(host, "/*:@ ") {
				return errors.New("commit: exact database hostnames are required")
			}
			policy.Hosts[host] = true
		}
		for _, value := range strings.Split(cidrs, ",") {
			prefix, err := netip.ParsePrefix(strings.TrimSpace(value))
			if err != nil {
				return errors.New("commit: database address prefixes are required")
			}
			policy.Prefixes = append(policy.Prefixes, prefix.Masked())
		}
	}
	cycles := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "schedd_commit_relay_cycles_total", Help: "Managed customer outbox relay source cycles by bounded result code."}, []string{"result"})
	accepted := prometheus.NewCounter(prometheus.CounterOpts{Name: "schedd_commit_relay_accepted_total", Help: "Customer outbox events checkpointed after durable acceptance."})
	if err := registry.Register(cycles); err != nil {
		return err
	}
	if err := registry.Register(accepted); err != nil {
		return err
	}
	if err := registry.Register(commitmanaged.NewObservationCollector(store)); err != nil {
		return err
	}
	cleanupRows := prometheus.NewCounter(prometheus.CounterOpts{Name: "schedd_commit_cleanup_rows_total", Help: "Expired accepted outbox rows deleted by this scheduler."})
	cleanupPasses := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "schedd_commit_cleanup_passes_total", Help: "Cleanup passes by bounded outcome."}, []string{"result"})
	cleanupDuration := prometheus.NewHistogram(prometheus.HistogramOpts{Name: "schedd_commit_cleanup_duration_seconds", Help: "Time spent in source cleanup per pass.", Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2}})
	for _, metric := range []prometheus.Collector{cleanupRows, cleanupPasses, cleanupDuration} {
		if err := registry.Register(metric); err != nil {
			return err
		}
	}
	receiptStorage := commitmanaged.NewReceiptStorageCollector(store)
	if err := registry.Register(receiptStorage); err != nil {
		return err
	}
	realtimeEnabled := os.Getenv("FAAS_REALTIME_TRANSACTIONAL_PREVIEW_ENABLED")
	if realtimeEnabled != "" && realtimeEnabled != "0" && realtimeEnabled != "1" {
		return errors.New("commit: FAAS_REALTIME_TRANSACTIONAL_PREVIEW_ENABLED must be 0 or 1")
	}
	manager := &commitmanaged.Manager{
		RealtimeEnabled: realtimeEnabled == "1", ReserveHealthyWorker: reserveHealthy == "true",
		PublicDatabases: publicDatabases, Store: store, Identities: identities, Policy: policy, Report: func(source, code string, n int) {
			cycles.WithLabelValues(code).Inc()
			accepted.Add(float64(n))
			if code != "healthy" && code != "reserved_connect_deferred" {
				log.Warn("commit relay requires recovery", "source_id", source, "code", code)
			}
		}, ReportCleanup: func(rows int64, duration time.Duration, result string) {
			cleanupRows.Add(float64(rows))
			cleanupPasses.WithLabelValues(result).Inc()
			cleanupDuration.Observe(duration.Seconds())
		}}
	if fastReservedConnect == "true" {
		manager.ReservedConnectBudget = commitmanaged.FastReservedConnectBudget
	}
	manager.AdaptiveReservedConnectMax = adaptiveReservedConnectMax
	go receiptStorage.Run(ctx)
	go func() { _ = manager.Run(ctx) }()
	return nil
}
