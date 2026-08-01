// Build-enqueuer seam for githubd's push-dispatch path (PR-GH.5,
// repo decomposition Phase 5).
//
// PR-GH.4 wired the push-dispatch path through
// pkg/reconcile.Service so a push reconciles the project
// (apps rows added/changed/removed). PR-GH.5 fans the reconcile
// result out into per-app builds: every app in Result.Added ∪
// Result.Changed gets a build enqueued via the BuildEnqueuer
// seam.
//
// FAN-OUT SCOPE (intentional, addressed in a follow-up):
//
// The current implementation is a naive FULL fan-out: every
// touched app is rebuilt regardless of which files changed.
// This is correct (correct = "rebuild what the user pushed to")
// but conservative. A path-filtered follow-up (compare/{base}
// ...{head} per push, only rebuild apps whose RootDir changed
// files) is deferred per ADR-050 — the loader fan-out
// throughput is enough for v1.0 volumes. A TODO at the
// call site points at the ADR-050 reference.
//
// Issue #432 phase 5 (repo-deploy close-the-loop): the seam
// was extended to carry the staged source path / source URL /
// source bytes / repo provenance as a BuildSpec struct. The
// production implementation is the apidEnqueuer in
// cmd/githubd/state_enqueue.go; the noopEnqueuer stays as the
// test-only seam (the 11 tests in pkg/githubd/service_test.go
// that use a recordingEnqueuer keep working unchanged because
// they only assert on the returned build_id).
//
// RETRY + PARTIAL-SUCCESS:
// Enqueue failures are best-effort (logged, not returned).
// The webhooks HTTP contract is "200 OK with deployment_id"
// on success; partial-success is rendered as
// {reconciled: true, build_ids: [...]} with a separate
// metric counter for enqueue failures. The alternative
// (failing the entire push because one of 50 builds was
// rejected) is worse for the customer.

package githubd

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// BuildSpec carries everything a build needs from the push
// dispatcher. The noopEnqueuer ignores it; the apidEnqueuer
// passes the staged source path + provenance URL + commit SHA
// to githubd's RealService.CreateDeploymentFromPush via the
// apid gRPC bridge (issue #432 phase 5).
//
// The state.App reference carries ID, RootDir, WorkloadName,
// AccountID — the dispatcher fills these in from the reconcile
// result + the binding row. The SourcePath is the absolute
// path to the per-app .tar.gz on disk (githubd stages each
// app's RootDir subtree into <FAAS_GITHUBD_WORK_DIR>/build-sources/
// before the enqueue loop). SourceURL is the upstream archive
// URL (the codeload tarball githubd pulled) — provenance-only,
// builderd never fetches it. SourceBytes is the on-disk size
// of the staged tarball.
type BuildSpec struct {
	App          state.App
	CommitSHA    string
	RepoFullName string
	Ref          string
	Branch       string
	Pusher       string
	SourcePath   string
	SourceURL    string
	SourceBytes  int64
}

// BuildEnqueuer is the seam githubd uses to schedule a build
// for one (app, commit) pair. The production implementation
// (apidEnqueuer) calls the apid gRPC bridge, which writes the
// deployment + build rows and emits the build_queued pg_notify
// that builderd LISTENs on. The noopEnqueuer is the test +
// pre-issue-#432 default — it mints a synthetic build_id so
// the wire contract is exercised end-to-end without a real
// bridge. The production wiring in cmd/githubd/main.go swaps
// the noopEnqueuer for the apidEnqueuer.
type BuildEnqueuer interface {
	// Enqueue schedules a build for the given spec. The
	// returned state.Build carries the (durable) build_id
	// the apid-side bridge minted and the deployment_id
	// the build row points at. Errors are returned to the
	// caller — the dispatcher decides whether to fail the
	// push or treat the missing build as a soft failure.
	Enqueue(ctx context.Context, spec BuildSpec) (state.Build, error)
}

// noopEnqueuer is the test + pre-issue-#432 default. It mints a
// deterministic buildID from the (app, commit) pair so the wire
// contract is pin-able without a real bridge backend. The
// follow-up PR (issue #432 phase 5) swaps the production wiring
// for the apidEnqueuer in cmd/githubd/main.go without touching
// this noop.
//
// Production-shape contract (issue #432 phase 5 review
// follow-up): the returned state.Build always carries
//
//   - ID: "noop-build-<UUIDv7>" — synthetic, never persisted
//   - Kind: state.DeploymentKindGitHub — matches the production
//     apidEnqueuer's output so test assertions that filter on
//     Kind don't see a false negative
//   - DeploymentID: "" — the noop does not own the deployment
//     row; the apidEnqueuer fills this from the bridge response
//
// Tests that assert on DeploymentID must use a stub returning
// a real ID (recordingEnqueuer in service_test.go does this).
type noopEnqueuer struct {
	log *slog.Logger
}

// NewNoopEnqueuer returns a BuildEnqueuer that mints a
// synthetic buildID per call. The ID is a UUIDv7 prefixed
// with "noop-build-" so dashboards can filter out fake IDs
// from the real bridge. The ID is NOT persistent — a daemon
// restart renumbers all builds; the prefix lets §12 dashboards
// exclude synthetic builds from real-build throughput metrics.
func NewNoopEnqueuer(log *slog.Logger) BuildEnqueuer {
	if log == nil {
		log = slog.Default()
	}
	return &noopEnqueuer{log: log}
}

// Enqueue mints the synthetic buildID. UUIDv7 keeps IDs
// roughly time-ordered (so build log pages sort naturally)
// while staying unique per call — the prior deterministic
// "build-<app>-<sha>" composition collided when an
// installation re-pushed the same commit twice, and the
// "build-" prefix read like a real build ID in dashboards.
func (n *noopEnqueuer) Enqueue(ctx context.Context, spec BuildSpec) (state.Build, error) {
	u, err := uuid.NewV7()
	if err != nil {
		return state.Build{}, fmt.Errorf("noop enqueuer: uuid v7: %w", err)
	}
	bid := "noop-build-" + u.String()
	n.log.Info("noop enqueuer: synthetic build",
		"build_id", bid, "app_id", spec.App.ID, "commit_sha", spec.CommitSHA, "account_id", spec.App.AccountID)
	return state.Build{ID: bid, Kind: state.DeploymentKindGitHub}, nil
}
