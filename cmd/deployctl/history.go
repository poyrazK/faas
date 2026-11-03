package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
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

// historySpoolPath is where a compute host spools its activations.
// scripts/ci/unit_deploy.sh (record) reads the same path.
const historySpoolPath = "/var/lib/faas/deploy/activations.jsonl"

// StartActivation records a deploy or rollback in the platform's release
// history (issue #574, deploycontroller.ActivationRecorder). The history is
// best effort: a failed write only warns. On the first rollout the table
// does not exist until the release's migrations have run, so the start
// fails and FinishActivation records the whole activation instead.
func (r hostRuntime) StartActivation(ctx context.Context, a deploycontroller.Activation) string {
	if r.spoolsHistory(a) || !r.recordsHistory(a) {
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

// FinishActivation records how the activation ended. A compute host spools
// it for CD to import on the control plane.
func (r hostRuntime) FinishActivation(ctx context.Context, id string, a deploycontroller.Activation, status, detail string) {
	if r.spoolsHistory(a) {
		if err := r.spoolActivation(a, status, detail); err != nil {
			fmt.Fprintf(os.Stderr, "deployctl: release history: spool %s %s as %s: %v\n", a.Kind, a.ReleaseID, status, err)
		}
		return
	}
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
// Compute hosts never migrate and have no local Postgres; their release
// installs are recorded from compute_nodes.release_id by a trigger
// (migration 20261029083739103), and their per-unit activations through the
// spool.
func (r hostRuntime) recordsHistory(a deploycontroller.Activation) bool {
	return r.databaseURL != "" && !hostManagedUnits(a.ReleaseRoot)
}

// spoolsHistory reports whether this activation goes to the spool: a
// per-unit release on a compute host.
func (r hostRuntime) spoolsHistory(a deploycontroller.Activation) bool {
	return r.historySpool != "" && hostManagedUnits(a.ReleaseRoot)
}

func (r hostRuntime) spoolActivation(a deploycontroller.Activation, status, detail string) error {
	h := r.historyActivation(a)
	return releasehistory.AppendSpool(r.historySpool, releasehistory.Line{
		ID: uuid.NewString(), Host: h.Host, Kind: h.Kind, ReleaseID: h.ReleaseID, CommitSHA: h.CommitSHA,
		PreviousReleaseID: h.PreviousReleaseID, Services: h.Services, Actor: h.Actor,
		Status: status, Detail: detail, StartedAt: h.StartedAt, FinishedAt: time.Now().UTC(),
	})
}

// runRecord imports a compute host's spooled activations into the history,
// under --host: the node's compute_nodes name, which CD knows. A spool never
// names its own host, so a node cannot write rows for another. It runs on the
// control plane, where deployctl reaches Postgres as the schema owner, and
// reads the spool from stdin.
func runRecord(args []string) error {
	fs := flag.NewFlagSet("record", flag.ContinueOnError)
	host := fs.String("host", "", "the compute host the spool came from (its compute_nodes name)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || !recordHostPattern.MatchString(*host) {
		return fmt.Errorf("usage: deployctl record --host <node> < activations.jsonl")
	}
	lines, err := releasehistory.ReadSpool(os.Stdin)
	if err != nil {
		return err
	}
	added, err := defaultHostRuntime().importSpool(context.Background(), *host, lines)
	if err != nil {
		return fmt.Errorf("release history: %w", err)
	}
	fmt.Printf("release history: %d new of %d spooled activations for %s\n", added, len(lines), *host)
	return nil
}

// recordHostPattern admits node names as compute_nodes keeps them.
var recordHostPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,252}$`)

func (r hostRuntime) importSpool(ctx context.Context, host string, lines []releasehistory.Line) (int, error) {
	added := 0
	err := r.withHistory(ctx, func(ctx context.Context, store *releasehistory.Store) error {
		for _, l := range lines {
			l.Host = host
			isNew, err := store.Import(ctx, l)
			if err != nil {
				return err
			}
			if isNew {
				added++
			}
		}
		return nil
	})
	return added, err
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
