package main

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"os"
	"strings"

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
	policy := commitwork.NetworkPolicy{Hosts: map[string]bool{}}
	for _, host := range strings.Split(os.Getenv("FAAS_COMMIT_DATABASE_HOSTS"), ",") {
		host = strings.TrimSpace(host)
		if host == "" || strings.ContainsAny(host, "/*:@ ") {
			return errors.New("commit: exact database hostnames are required")
		}
		policy.Hosts[host] = true
	}
	for _, value := range strings.Split(os.Getenv("FAAS_COMMIT_DATABASE_CIDRS"), ",") {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(value))
		if err != nil {
			return errors.New("commit: database address prefixes are required")
		}
		policy.Prefixes = append(policy.Prefixes, prefix.Masked())
	}
	cycles := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "schedd_commit_relay_cycles_total", Help: "Managed customer outbox relay source cycles by bounded result code."}, []string{"result"})
	accepted := prometheus.NewCounter(prometheus.CounterOpts{Name: "schedd_commit_relay_accepted_total", Help: "Customer outbox events checkpointed after durable acceptance."})
	if err := registry.Register(cycles); err != nil {
		return err
	}
	if err := registry.Register(accepted); err != nil {
		return err
	}
	manager := &commitmanaged.Manager{Store: store, Identities: identities, Policy: policy, Report: func(source, code string, n int) {
		cycles.WithLabelValues(code).Inc()
		accepted.Add(float64(n))
		if code != "healthy" {
			log.Warn("commit relay requires recovery", "source_id", source, "code", code)
		}
	}}
	go func() { _ = manager.Run(ctx) }()
	return nil
}
