package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/deploycontroller"
	"github.com/onebox-faas/faas/pkg/releasehistory"
)

// historyTimeout bounds each release-history write, so an unreachable or
// stuck Postgres delays a deploy or rollback by seconds, never blocks it.
const historyTimeout = 5 * time.Second

// deployActorEnv names who runs deployctl in the release history. CD sets it
// to its workflow run; an operator's sudo or login name stands in otherwise.
const deployActorEnv = "FAAS_DEPLOY_ACTOR"

// StartActivation records a deploy or rollback in the platform's release
// history (issue #574, deploycontroller.ActivationRecorder). The history is
// best effort: a failed write only warns. On the first rollout the table
// does not exist until the release's migrations have run, so the start
// fails and FinishActivation records the whole activation instead.
func (r hostRuntime) StartActivation(ctx context.Context, a deploycontroller.Activation) string {
	if !r.recordsHistory(a) {
		return ""
	}
	var id string
	err := r.withHistory(ctx, func(ctx context.Context, store *releasehistory.Store) error {
		var err error
		id, err = store.Start(ctx, r.historyActivation(a))
		return err
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "deployctl: release history: record the start of %s %s: %v; recording it when it finishes\n", a.Kind, a.ReleaseID, err)
	}
	return id
}

// FinishActivation records how the activation ended.
func (r hostRuntime) FinishActivation(ctx context.Context, id string, a deploycontroller.Activation, status, detail string) {
	if !r.recordsHistory(a) {
		return
	}
	err := r.withHistory(ctx, func(ctx context.Context, store *releasehistory.Store) error {
		return store.Finish(ctx, id, r.historyActivation(a), status, detail, time.Now().UTC())
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "deployctl: release history: record %s %s as %s: %v\n", a.Kind, a.ReleaseID, status, err)
	}
}

// recordsHistory reports whether this host keeps the history: a control-plane
// host, which reaches Postgres as the schema owner over the local socket.
// Compute hosts never migrate and have no local Postgres; their current
// release is compute_nodes.release_id.
func (r hostRuntime) recordsHistory(a deploycontroller.Activation) bool {
	return r.databaseURL != "" && !hostManagedUnits(a.ReleaseRoot)
}

func (r hostRuntime) historyActivation(a deploycontroller.Activation) releasehistory.Activation {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	var services []string
	if r.only != nil {
		services = make([]string, 0, len(r.only))
		for name := range r.only {
			services = append(services, name)
		}
		sort.Strings(services)
	}
	return releasehistory.Activation{
		Host:              host,
		Kind:              a.Kind,
		ReleaseID:         a.ReleaseID,
		CommitSHA:         a.CommitSHA,
		PreviousReleaseID: a.Previous,
		Services:          services,
		Actor:             deployActor(),
		StartedAt:         a.StartedAt,
	}
}

func deployActor() string {
	if actor := strings.TrimSpace(os.Getenv(deployActorEnv)); actor != "" {
		return actor
	}
	for _, name := range []string{"SUDO_USER", "USER", "LOGNAME"} {
		if user := strings.TrimSpace(os.Getenv(name)); user != "" {
			return "operator:" + user
		}
	}
	return "operator:unknown"
}

// withHistory runs fn on a short-lived connection. The deploy's context may
// already be cancelled when an outcome is recorded; the write still runs,
// bounded by historyTimeout.
func (r hostRuntime) withHistory(ctx context.Context, fn func(context.Context, *releasehistory.Store) error) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), historyTimeout)
	defer cancel()
	cfg, err := pgx.ParseConfig(r.databaseURL)
	if err != nil {
		return fmt.Errorf("parse database URL: %w", err)
	}
	cfg.RuntimeParams["application_name"] = "deployctl"
	cfg.RuntimeParams["statement_timeout"] = fmt.Sprint(historyTimeout.Milliseconds())
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	return fn(ctx, releasehistory.NewStore(conn))
}
