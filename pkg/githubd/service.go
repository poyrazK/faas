// githubd service (spec §14 M7.5, ADR-012, ADR-050).
//
// Service is the business-logic core of the githubd daemon. It
// implements the gRPC contract (see pkg/githubdgrpc/server.go) and
// the loopback HTTP webhook handler. PR-H (mega-PR-GH of repo
// decomposition Phase 5) rewrites the push-dispatch path through
// pkg/reconcile.Service so githubd and apid share a single workload-
// mutation primitive. The legacy CreateDeployment function-typed
// seam (slice 7) is retired in this commit.
//
// Service is constructed by cmd/githubd/main.go and shared across
// the gRPC server + the HTTP webhook listener.
package githubd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	githubdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/githubd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/reconcile"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// filterMode values for path-filtered build fan-out (ADR-050 §103-109).
// Lifted to constants so deployment + filter tests + the
// summary log all reference the same identifiers.
const (
	filterModePaths        = "paths"
	filterModeFullFallback = "full_fallback"
)

// AppBindingStore is the slice of store githubd reads to look up
// (repo → app) bindings for incoming pushes. PR-H widens the
// return type from githubdgrpc.AppBinding to state.GitHubBinding so
// the push-dispatch path can read AccountID + InstallID without a
// second round-trip — the bind row already carries both fields,
// and the state adapter (cmd/githubd/state_adapter.go:91) returns
// the full state row verbatim.
//
// PR-H retires the AppBinding struct in githubdgrpc — the only
// remaining caller is cmd/gatewayd-internal/end_to_end_test.go, which gets
// updated to the new return shape in this commit.
type AppBindingStore interface {
	GetAppBinding(ctx context.Context, repoFullName, branch string) (state.GitHubBinding, error)
}

type InstallationAppBindingStore interface {
	GetAppBindingForInstallation(ctx context.Context, repoFullName, branch string, installationID int64) (state.GitHubBinding, error)
}

func appBindingForEvent(ctx context.Context, bindings AppBindingStore, repoFullName, branch string, installationID int64) (state.GitHubBinding, error) {
	if installationID > 0 {
		if scoped, ok := bindings.(InstallationAppBindingStore); ok {
			return scoped.GetAppBindingForInstallation(ctx, repoFullName, branch, installationID)
		}
	}
	return bindings.GetAppBinding(ctx, repoFullName, branch)
}

// InstallsLookup is the read seam githubd uses to resolve a
// GitHub App installation row by account ID. The store-backed
// adapter (stateInstallsAdapter.ForAccount) is the production
// implementation; tests inject a stub that returns a fixed
// GitHubInstall with a sealed token.
//
// Legacy implementations are keyed on AccountID. Production also implements
// ScopedInstallsLookup and resolves the exact installation carried by the
// authenticated webhook and binding.
type InstallsLookup interface {
	ForAccount(ctx context.Context, accountID string) (state.GitHubInstall, error)
}

type ScopedInstallsLookup interface {
	ForAccountInstallation(ctx context.Context, accountID string, installationID int64) (state.GitHubInstall, error)
}

func installForAccount(ctx context.Context, installs InstallsLookup, accountID string, installationID int64) (state.GitHubInstall, error) {
	if installationID > 0 {
		if scoped, ok := installs.(ScopedInstallsLookup); ok {
			return scoped.ForAccountInstallation(ctx, accountID, installationID)
		}
	}
	return installs.ForAccount(ctx, accountID)
}

// WriteCheck is the seam githubd uses to push build-phase updates
// back to GitHub. Slice 8 fills this in with the real Checks writer;
// slice 7 leaves it as a stub that records the call into the log
// so the smoke test can assert on the order.
type WriteCheck func(ctx context.Context, repoFullName, commitSHA string, phase githubdgrpc.CheckPhase) error

type WriteAppCheckFunc func(ctx context.Context, installationID int64, repoFullName, commitSHA, appSlug string, phase githubdgrpc.CheckPhase, summary string) error

// WriteScopedAppCheckFunc is the scope-aware Check Run seam. The legacy
// WriteAppCheckFunc remains supported for embedders that do not need named
// environments yet.
type WriteScopedAppCheckFunc func(ctx context.Context, installationID int64, repoFullName, commitSHA, appSlug, scope string, phase githubdgrpc.CheckPhase, summary string) error

// WriteSkippedCheckForInstallationFunc writes the neutral production Check
// Run used when a commit explicitly opts out of deployment.
type WriteSkippedCheckForInstallationFunc func(ctx context.Context, installationID int64, repoFullName, commitSHA, summary string) error

// WritePreviewCheck is the seam githubd uses to push PR-preview
// Check Run updates back to GitHub (issue #272 / ADR-094). Wired
// by cmd/githubd/main.go to ChecksAPI.WritePreviewCheck; nil-safe
// (handlePullRequest logs + proceeds when nil, mirroring the
// production WriteCheck seam).
//
// The phase + previewURL pair mirrors the production Check Run
// shape: phase drives the GitHub (status, conclusion) tuple via
// phaseToStatus/Conclusion; previewURL is appended to the
// summary as a Markdown link so the PR author can click through.
type WritePreviewCheck = WritePreviewCheckFunc

// WritePreviewCheckForkRefused is the seam githubd uses to push
// the D3 fork-refused neutral Check Run. Wired by
// cmd/githubd/main.go to ChecksAPI.WritePreviewCheckForkRefused.
// nil-safe.
type WritePreviewCheckForkRefused = WritePreviewCheckForkRefusedFunc

// WritePreviewDestroyComment is the seam githubd uses to push a
// one-time PR-thread comment carrying the dashboard's one-click
// destroy link (issue #961 Mega-C PR-1, leaf 3). Wired by
// cmd/githubd/main.go to ChecksAPI.WritePreviewDestroyComment;
// nil-safe (handlePullRequest logs + proceeds when nil).
type WritePreviewDestroyComment = WritePreviewDestroyCommentFunc

// Service is the business-logic object shared across the HTTP
// webhook handler and the gRPC server. nil fields fall back to
// safe no-ops (so partial deployments degrade gracefully until
// every dependency is wired). The Reconcile + Source + Installs
// fields are required for HandlePushRequest to reach the
// reconcile step; the production wiring in cmd/githubd sets
// all three from a single boot path. The Enqueuer field is
// the PR-GH.5 build fan-out seam; nil falls back to a
// noopEnqueuer so the unit tests don't have to wire an
// enqueuer just to exercise the reconcile path.
//
// ChangedFiles is the PR-GH.6 path-filter seam: when set,
// HandlePushRequest queries GitHub's compare API between
// ev.Before and ev.After and rebuilds only the apps whose
// RootDir intersects the changed file set. nil falls back to
// the naive "rebuild every touched app" loop — this is the
// test-rig path; production wires either the real client
// (NewHTTPChangedFiles wrapped in NewBreakerChangedFiles) or
// NewUnavailableChangedFiles on credentials-missing branches
// so the dispatcher surfaces the "no credentials" case via the
// mode="error" / mode="breaker_open" metric labels rather than
// silently downgrading to full fan-out.
type Service struct {
	Log                              *slog.Logger
	Bindings                         AppBindingStore
	Installs                         InstallsLookup
	Source                           SourceFetcher
	Reconcile                        *reconcile.Service
	Enqueuer                         BuildEnqueuer
	ChangedFiles                     ChangedFilesClient
	WriteCheck                       WriteCheck
	WriteAppCheck                    WriteAppCheckFunc
	WriteScopedAppCheck              WriteScopedAppCheckFunc
	WriteSkippedCheckForInstallation WriteSkippedCheckForInstallationFunc
	// Ops is the per-daemon Prometheus facade. Used by the
	// push-dispatch path to increment
	// githubd_path_filter_total{mode} after lookupChangedFiles
	// picks a mode (issue #432 phase 5 / ADR-050 §109). nil
	// is allowed — the Observe* accessors are nil-safe — so
	// unit tests that don't wire metrics keep working
	// unchanged. Production wiring in cmd/githubd/main.go
	// sets this from wire.NewOpsMetrics("githubd").
	Ops *wire.OpsMetrics
	// WritePreviewCheck is the PR-preview Check Run writer
	// (issue #272 / ADR-094). Wired to ChecksAPI.WritePreviewCheck
	// in cmd/githubd/main.go; nil-safe (handlePullRequest logs
	// + proceeds when nil).
	WritePreviewCheck                WritePreviewCheck
	WritePreviewCheckForInstallation WritePreviewCheckForInstallationFunc
	// WritePreviewCheckForkRefused is the D3 fork-refused
	// neutral Check Run writer. Same nil-safe posture as
	// WritePreviewCheck.
	WritePreviewCheckForkRefused                WritePreviewCheckForkRefused
	WritePreviewCheckForkRefusedForInstallation WritePreviewCheckForkRefusedForInstallationFunc
	// WritePreviewDestroyComment is the one-time PR-thread
	// destroy-hint writer (issue #961 Mega-C PR-1, leaf 3).
	// Same nil-safe posture as WritePreviewCheck. The dedupe
	// carrier is apps.preview_destroy_commented_at — set
	// BEFORE the POST so a duplicate enqueue (e.g. close +
	// reopen in the same webhook burst) collapses to a
	// single comment.
	WritePreviewDestroyComment WritePreviewDestroyComment
	// WritePreviewCommentForInstallation upserts the stable PR-thread
	// preview status comment. It is installation-scoped so a repository
	// can never accidentally publish another customer's preview details.
	WritePreviewCommentForInstallation WritePreviewCommentForInstallationFunc
	// WorkDir is the root directory under which githubd
	// stages the per-app source tarballs that the apid
	// bridge passes to builderd. Defaults to /var/lib/faas/
	// githubd at runtime (cmd/githubd/main.go:githubdWorkDir).
	// Empty in tests — the staging step is skipped when
	// WorkDir is unset (matches the pre-issue-#432 fan-out
	// behaviour; tests that don't care about source staging
	// keep working unchanged).
	WorkDir string
}

// NewService builds a Service. Tests inject fakes for the seams;
// production wires the live implementations in cmd/githubd/main.go.
func NewService(log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{Log: log}
}

type webhookDeliveryContextKey struct{}

// HandleWebhookDelivery dispatches a durable inbox item. DeliveryID is kept
// out of the GitHub JSON body and carried through context solely to stamp the
// downstream per-app enqueue idempotency key.
func (s *Service) HandleWebhookDelivery(ctx context.Context, delivery WebhookDelivery) error {
	if delivery.DeliveryID != "" {
		ctx = context.WithValue(ctx, webhookDeliveryContextKey{}, delivery.DeliveryID)
	}
	return s.HandleWebhookEvent(ctx, delivery.EventType, delivery.Payload)
}

func webhookDeliveryID(ctx context.Context) string {
	deliveryID, _ := ctx.Value(webhookDeliveryContextKey{}).(string)
	return deliveryID
}

// HandleWebhookEvent routes the standard X-GitHub-Event value. Keeping the
// router beside the business service prevents the HTTP transport and durable
// worker from drifting into push-only behavior.
func (s *Service) HandleWebhookEvent(ctx context.Context, eventType string, body []byte) error {
	switch eventType {
	case "push":
		event, err := DecodePush(body)
		if err != nil {
			return err
		}
		if event.Installation.ID <= 0 {
			return errors.New("githubd: push missing installation.id")
		}
		_, err = s.HandlePushRequest(ctx, body)
		return err
	case "pull_request":
		event, err := DecodePullRequest(body)
		if err != nil {
			return err
		}
		if event.Installation.ID <= 0 {
			return errors.New("githubd: pull_request missing installation.id")
		}
		_, err = s.handlePullRequest(ctx, body)
		return err
	default:
		return fmt.Errorf("githubd: unsupported webhook event %q", eventType)
	}
}

// HandlePushRequest is the HTTP webhook entry point. It verifies
// the signature (the proxy already did HMAC verify on the edge;
// this is a defense-in-depth check), decodes the body, resolves the
// (repo, branch) → project binding, fetches the source tree, runs
// reposcan.Scan, and dispatches the result through
// reconcile.Service.Reconcile.
//
// Returns:
//
//   - reconcile.Result on success. The webhook HTTP handler
//     serialises Result.Added ∪ Result.Changed into the
//     {status, deployment_id} response body (naive fan-out: every
//     touched app is its own deployment).
//   - ErrNoBinding (sentinel) when the push doesn't match any
//     binding OR the binding's project row is missing. The HTTP
//     handler turns this into a 200 with an ignored-payload body
//     so GitHub does not retry.
//   - ErrIgnored (sentinel) when the production-branch guard
//     tripped. The HTTP handler returns 200 with
//     {status:ignored, reason:feature_branch}.
//   - any other error → 500 (logged with op context).
//
// The source tree's lifecycle is owned by HandlePushRequest:
// the deferred Close runs on the success branch of
// s.Source.Fetch (a failed Fetch returns early without a
// dangling tree reference). The panic path is handled by
// the runtime's deferred-unwind — Go runs deferreds even
// on panic, so a panicking reconcile still releases the
// temp dir.
func (s *Service) HandlePushRequest(ctx context.Context, body []byte) (reconcile.Result, error) {
	ev, err := DecodePush(body)
	if err != nil {
		return reconcile.Result{}, err
	}
	if ev.Deleted {
		// GitHub represents a deleted branch or tag with an all-zero
		// `after` SHA. Never feed that sentinel into source fetch or
		// reconcile; deletion is not a deploy trigger.
		return reconcile.Result{}, ErrNoBinding
	}
	branch := refToBranch(ev.Ref)
	isTag := false
	if branch == "" {
		tag := refToTag(ev.Ref)
		if tag == "" {
			// Pull requests and other Git refs are not production
			// deployment triggers.
			return reconcile.Result{}, ErrNoBinding
		}
		if err := validateReleaseTag(tag, ev.Before, ev.Created, ev.Forced); err != nil {
			// Release tags are a one-way promotion boundary. Reject
			// malformed tags and moved tags before binding lookup,
			// source fetch, or reconcile so an ignored delivery cannot
			// cause any customer-side work.
			return reconcile.Result{}, err
		}
		// A tag has no branch binding of its own. Resolve it against
		// the repository's default branch; the normal reconcile guard
		// still verifies that this is the configured production branch.
		branch = ev.Repository.DefaultBranch
		if branch == "" {
			branch = defaultProductionBranch
		}
		isTag = true
	}

	// 1. Resolve the (repo, branch) binding. An empty BindingID
	// is the canonical "no row" shape; ErrNoBinding covers both
	// "store said not-found" and "store returned an empty row".
	binding, err := appBindingForEvent(ctx, s.Bindings, ev.Repository.FullName, branch, ev.Installation.ID)
	if (err != nil || binding.BindingID == "") && !isTag && s.Reconcile != nil && s.Reconcile.Store != nil {
		// A mapped non-production branch has no row in the legacy
		// repo+production_branch binding index. Resolve the project by
		// repository and synthesize the same binding shape so branch
		// routing remains additive and old bindings continue to work.
		project, projectErr := s.Reconcile.Store.ProjectByRepo(ctx, "", ev.Installation.ID, ev.Repository.FullName)
		if projectErr == nil && project.AccountID != "" && project.ProductionBranch != "" {
			binding = state.GitHubBinding{
				BindingID:        "project-" + project.ID,
				AccountID:        project.AccountID,
				InstallID:        project.InstallID,
				RepoFullName:     project.RepoFullName,
				ProductionBranch: project.ProductionBranch,
			}
			err = nil
		}
	}
	if (err != nil || binding.BindingID == "") && isTag && s.Reconcile != nil && s.Reconcile.Store != nil {
		// A repository may intentionally deploy from a non-default
		// production branch. The project row is the authoritative
		// (installation, repository, production_branch) mapping, so
		// use it as a tag fallback when the default-branch binding
		// lookup cannot match.
		project, projectErr := s.Reconcile.Store.ProjectByRepo(ctx, "", ev.Installation.ID, ev.Repository.FullName)
		if projectErr == nil && project.AccountID != "" && project.ProductionBranch != "" {
			binding = state.GitHubBinding{
				BindingID:        "project-" + project.ID,
				AccountID:        project.AccountID,
				InstallID:        project.InstallID,
				RepoFullName:     project.RepoFullName,
				ProductionBranch: project.ProductionBranch,
			}
			branch = project.ProductionBranch
			err = nil
		}
	}
	if err != nil {
		return reconcile.Result{}, ErrNoBinding
	}
	if binding.BindingID == "" || binding.AccountID == "" {
		return reconcile.Result{}, ErrNoBinding
	}
	if ev.Installation.ID > 0 && binding.InstallID != ev.Installation.ID {
		return reconcile.Result{}, ErrNoBinding
	}

	// 2. Resolve the install row. The account → install mapping
	// is one-to-one (every account has at most one GitHub App
	// install). state.ErrNotFound surfaces as ErrNoBinding so the
	// webhook handler renders the same ignored shape.
	install, err := installForAccount(ctx, s.Installs, binding.AccountID, binding.InstallID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return reconcile.Result{}, ErrNoBinding
		}
		return reconcile.Result{}, fmt.Errorf("githubd: resolve install: %w", err)
	}
	if install.AccountID == "" || install.InstallationID == 0 {
		// Defensive: the install row exists but is incomplete.
		// The OAuth handshake should never write a partial row,
		// but a manual SQL edit could.
		return reconcile.Result{}, ErrNoBinding
	}

	// 3. Resolve the project row. ProjectByRepo is the
	// push-dispatch lookup from PR-F; missing projects map to
	// ErrNoBinding (a bind row without a project is a soft-
	// deleted binding — ignore the push).
	project, err := s.Reconcile.Store.ProjectByRepo(ctx, binding.AccountID, install.InstallationID, ev.Repository.FullName)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return reconcile.Result{}, ErrNoBinding
		}
		return reconcile.Result{}, fmt.Errorf("githubd: resolve project: %w", err)
	}

	// 3a. Takeover guard: the bind row's InstallID must match
	// the install row's InstallationID. If they diverge, the
	// binding points at a stale install (rotated webhook
	// secret, takeover/rebind flow) — treating that as a
	// push-dispatch would push the wrong repo's tree. We
	// map the divergence to ErrNoBinding so the webhook
	// handler renders the 200-ignored body. The fetcher's
	// internal (inst.InstallationID == installID) check is
	// a self-consistency guard for the install row only;
	// THIS check is the cross-table consistency guard.
	if binding.InstallID != install.InstallationID {
		s.Log.Warn("githubd: binding install_id mismatch",
			"binding_id", binding.BindingID,
			"binding_install_id", binding.InstallID,
			"install_installation_id", install.InstallationID,
			"repo", ev.Repository.FullName)
		return reconcile.Result{}, ErrNoBinding
	}

	// An explicit commit marker is a customer-controlled no-op. Resolve the
	// binding/install/project first so the neutral Check Run can use the
	// installation-scoped token, then stop before source fetch, scan, or
	// reconcile. Durable webhook deliveries treat ErrSkipDeploy as complete.
	if marker := ev.DeploySkipMarker(); marker != "" {
		s.Ops.ObserveGithubdPushSkipped(wire.PushSkippedReasonMarker)
		if s.WriteSkippedCheckForInstallation != nil {
			summary := fmt.Sprintf("Deployment skipped by commit marker %s.", marker)
			if checkErr := s.WriteSkippedCheckForInstallation(ctx, install.InstallationID, ev.Repository.FullName, ev.After, summary); checkErr != nil {
				return reconcile.Result{}, fmt.Errorf("githubd: write skipped check: %w", checkErr)
			}
		}
		return reconcile.Result{}, ErrSkipDeploy
	}

	deploymentScope, reconcileBranch, err := s.scopeForPush(ctx, project, branch)
	if err != nil {
		if errors.Is(err, ErrIgnored) {
			return reconcile.Result{}, ErrIgnored
		}
		return reconcile.Result{}, err
	}

	// 4. Fetch the source tree. The fetcher unseals the install
	// token internally (cmd/githubd/source_fetcher.go) and
	// returns a Tree whose Close() removes the temp dir.
	//
	// nil-safe: defer the Close on the success branch only.
	// Moving it here (after err-check) means a Fetch error
	// returns early with no defer; the success branch owns
	// the temp dir's lifecycle.
	tree, err := s.Source.Fetch(ctx, binding.AccountID, install.InstallationID, ev.Repository.FullName, ev.After)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("githubd: source fetch: %w", err)
	}
	defer func() { _ = tree.Close() }()

	// 5. Scan + reconcile. reposcan.Scan is wired on the
	// reconcile Service by NewService; tests inject a stub via
	// the Service.Scan field. The production-branch guard
	// returns ErrIgnored when the pushed branch differs from
	// project.ProductionBranch — the caller renders 200-ignored.
	scan, err := s.Reconcile.Scan(tree.FS())
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("githubd: scan: %w", err)
	}
	// githubd is push-driven (no --exclude analog on the webhook
	// path); pass nil so workloadDiff's exclude filter is a no-op.
	result, err := s.Reconcile.Reconcile(ctx, project, scan, ev.After, reconcileBranch, nil)
	if err != nil {
		if errors.Is(err, reconcile.ErrIgnored) {
			// Defensive: ErrIgnored is the Plan-side sentinel;
			// the apply path surfaces feature-branch via
			// Result.WasIgnored instead. The check stays so a
			// future guard that errors out with ErrIgnored
			// gets the same translation.
			result.WasIgnored = true
			return result, ErrIgnored
		}
		return result, err
	}
	// Production-branch guard trips without returning an error;
	// reconcile marks Result.WasIgnored so the caller can render
	// the 200-ignored body. Translate to ErrIgnored so the HTTP
	// handler can branch on the typed sentinel.
	if result.WasIgnored {
		return result, ErrIgnored
	}

	// 6. Build fan-out (PR-GH.6 path-filtered). Query GitHub's
	// compare API for the changed files between before and after,
	// then rebuild only the apps whose RootDir intersects that
	// set. Falls back to full fan-out on truncation, transport
	// error, empty-before, or any other compare failure
	// (ADR-050 §109). Path-filter is the default posture; the
	// full-fan-out fallback fires only when the compare API
	// itself fails.
	enqueuer := s.Enqueuer
	if enqueuer == nil {
		enqueuer = NewNoopEnqueuer(s.Log)
	}
	// Build selection starts from the full active project membership, not the
	// reconcile metadata delta. A source-only commit does not change an app row,
	// and a retried delivery sees an already-converged reconcile result; both
	// still need the same path-filtered deployment fan-out.
	touched, err := s.Reconcile.Store.AppsForProject(ctx, binding.AccountID, project.ID)
	if err != nil {
		return result, fmt.Errorf("githubd: list project apps for build fan-out: %w", err)
	}

	// Path-filter optimization (review #1): when the reconcile
	// step produced zero apps (empty touched set, e.g. a default-
	// branch push that matched the binding but Reconcile surfaced
	// no added/changed workloads), skip the GitHub compare-API
	// call entirely. There's nothing to filter, and the per-
	// installation API quota is too precious to burn on no-op
	// webhook deliveries. The empty-touched case is rare but
	// reachable (binding pointing at a project whose root has no
	// workloads, or a "no-op drift" reconcile that produced
	// Added=∅, Changed=∅).
	var changedFiles []string
	filterMode := filterModePaths
	if len(touched) > 0 {
		changedFiles, filterMode = s.lookupChangedFiles(ctx, ev, install.InstallationID)
	} else {
		s.Log.Debug("githubd: no touched apps; skipping compare-api call",
			"repo", ev.Repository.FullName, "sha", ev.After)
	}
	// Issue #432 phase 5 / ADR-050 §109: increment the
	// path-filter mode counter once per inbound webhook
	// after the mode is decided. The closed-set switch in
	// ObserveGithubdPathFilter drops unknown values silently;
	// filterMode is always one of {paths, full_fallback} from
	// this package, but the breaker may also push breaker_open
	// (handled below in lookupChangedFiles). Map the local
	// value to the wire label set:
	//
	//   - filterModePaths        → wire.PathFilterModePaths
	//   - filterModeFullFallback → wire.PathFilterModeFullFallback
	//
	// Truncated / Error are signalled by lookupChangedFiles'
	// own metric increments (it sees the raw error). The
	// single increment here covers the "no touched apps"
	// path which short-circuited lookupChangedFiles.
	s.ObserveFilterMode(filterMode)
	toEnqueue, skipped := s.filterByPath(touched, changedFiles, filterMode)
	deliveryID := webhookDeliveryID(ctx)

	// Legacy embeddings expose one repository-wide check. Production uses the
	// per-app writer below so monorepo workloads do not overwrite one another.
	// Durable deliveries let the deployment outbox write the initial queued
	// state; skipping the eager write prevents a reclaimed, already-live
	// delivery from regressing its Check Run back to queued.
	if deliveryID == "" && s.WriteAppCheck == nil && s.WriteScopedAppCheck == nil && s.WriteCheck != nil && len(toEnqueue) > 0 {
		if werr := s.WriteCheck(ctx, ev.Repository.FullName, ev.After, githubdgrpc.CheckPhaseQueued); werr != nil {
			s.Log.Warn("githubd: write queued check", "err", werr, "repo", ev.Repository.FullName, "sha", ev.After)
		}
	}

	buildIDs := make([]string, 0, len(toEnqueue))
	var deliveryErrors []error
	for _, app := range toEnqueue {
		if deliveryID == "" && (s.WriteAppCheck != nil || s.WriteScopedAppCheck != nil) {
			if werr := s.writeAppCheck(ctx, install.InstallationID, ev.Repository.FullName, ev.After,
				app.Slug, deploymentScope, githubdgrpc.CheckPhaseQueued, fmt.Sprintf("Deployment queued (scope: %s).", deploymentScope)); werr != nil {
				s.Log.Warn("githubd: write queued app check", "app_id", app.ID, "err", werr)
			}
		}
		// Stage the per-app source subtree into the githubd
		// workdir before the enqueue call. The apidEnqueuer
		// passes the staged path to the apid bridge as the
		// build's SourcePath (builderd reads it as a local
		// file — pkg/builderd/builderd.go:321). A staging
		// failure logs + skips the app (matches the partial-
		// success webhook contract).
		sourcePath, sourceBytes, sourceURL, stageErr := s.stageAppSource(ctx, tree, app, project, ev.After, branch)
		if stageErr != nil {
			s.Log.Warn("githubd: stage app source", "app_id", app.ID, "err", stageErr, "repo", ev.Repository.FullName, "sha", ev.After)
			if deliveryID == "" && (s.WriteAppCheck != nil || s.WriteScopedAppCheck != nil) {
				_ = s.writeAppCheck(ctx, install.InstallationID, ev.Repository.FullName, ev.After,
					app.Slug, deploymentScope, githubdgrpc.CheckPhaseFailed, fmt.Sprintf("Source staging failed (scope: %s): %s", deploymentScope, stageErr.Error()))
			}
			if deliveryID != "" {
				deliveryErrors = append(deliveryErrors, fmt.Errorf("stage app %s: %w", app.ID, stageErr))
			}
			continue
		}
		build, err := enqueuer.Enqueue(ctx, BuildSpec{
			App:          app,
			DeliveryID:   deliveryID,
			CommitSHA:    ev.After,
			RepoFullName: ev.Repository.FullName,
			Ref:          ev.Ref,
			Branch:       branch,
			Scope:        deploymentScope,
			Pusher:       ev.Pusher.Name,
			SourcePath:   sourcePath,
			SourceURL:    sourceURL,
			SourceBytes:  sourceBytes,
			// Issue #977 / ADR-116: explicit push event kind so the
			// bridge stamps DeploymentKindGitHub (legacy push path).
			// PRNumber + SenderLogin stay zero — push events don't
			// carry a pull_request webhook payload.
			EventKind: githubdpb.EnqueueBuildEventKind_EVENT_KIND_PUSH,
		})
		if err != nil {
			// Direct compatibility callers keep partial-success. Durable
			// delivery dispatch aggregates the failure after fan-out so
			// the inbox retries; already-successful apps are idempotent.
			s.Log.Warn("githubd: enqueue build", "app_id", app.ID, "err", err, "repo", ev.Repository.FullName, "sha", ev.After)
			if deliveryID == "" && (s.WriteAppCheck != nil || s.WriteScopedAppCheck != nil) {
				_ = s.writeAppCheck(ctx, install.InstallationID, ev.Repository.FullName, ev.After,
					app.Slug, deploymentScope, githubdgrpc.CheckPhaseFailed, fmt.Sprintf("Build enqueue failed (scope: %s): %s", deploymentScope, err.Error()))
			}
			if deliveryID != "" {
				deliveryErrors = append(deliveryErrors, fmt.Errorf("enqueue app %s: %w", app.ID, err))
			}
			continue
		}
		buildIDs = append(buildIDs, build.ID)
		// Issue #432 phase 5: emit project.build.enqueued
		// AFTER the bridge returns a non-empty build_id.
		// The durable build row is the source of truth, so
		// emitting on success keeps the audit paper trail
		// consistent with the build pipeline.
		if s.Reconcile != nil {
			s.Reconcile.EmitBuildEnqueued(ctx, project, app.ID, build.ID, build.DeploymentID, ev.After, ev.Repository.FullName, branch, sourcePath)
		}
	}
	result.BuildIDs = buildIDs
	if len(deliveryErrors) > 0 {
		return result, fmt.Errorf("githubd: delivery %s incomplete: %w", deliveryID, errors.Join(deliveryErrors...))
	}

	s.Log.Info("githubd push → reconcile",
		"repo", ev.Repository.FullName, "branch", branch,
		"tag", isTag,
		"sha", ev.After, "binding", binding.BindingID,
		"added", len(result.Added), "changed", len(result.Changed),
		"removed", len(result.Removed),
		"touched", len(touched), "rebuilt", len(buildIDs),
		"skipped", len(skipped), "filter_mode", filterMode,
		"files", len(changedFiles),
		"pusher", ev.Pusher.Name)
	return result, nil
}

// scopeForPush resolves a branch to its deployment scope. The production
// branch keeps the historical default scope when no explicit rule exists;
// every other branch must be explicitly mapped before it can deploy. The
// empty reconcile branch bypasses the legacy production-branch guard while
// BuildSpec.Branch still records the real Git ref for provenance.
func (s *Service) scopeForPush(ctx context.Context, project state.Project, branch string) (string, string, error) {
	scope := state.DefaultEnvScope
	reconcileBranch := branch
	if branchesStore, ok := s.Reconcile.Store.(state.ProjectDeployBranchesStore); ok {
		branches, err := branchesStore.ListProjectDeployBranches(ctx, project.ID)
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			return "", branch, fmt.Errorf("githubd: resolve deploy branch scope: %w", err)
		}
		if mapped, exists := branches[branch]; exists {
			scope = mapped
			if branch != project.ProductionBranch {
				reconcileBranch = ""
			}
			return scope, reconcileBranch, nil
		}
	}
	if branch != project.ProductionBranch {
		return "", branch, ErrIgnored
	}
	return scope, reconcileBranch, nil
}

func (s *Service) writeAppCheck(ctx context.Context, installationID int64, repoFullName, commitSHA, appSlug, scope string, phase githubdgrpc.CheckPhase, summary string) error {
	if s.WriteScopedAppCheck != nil {
		return s.WriteScopedAppCheck(ctx, installationID, repoFullName, commitSHA, appSlug, scope, phase, summary)
	}
	if s.WriteAppCheck != nil {
		return s.WriteAppCheck(ctx, installationID, repoFullName, commitSHA, appSlug, phase, summary)
	}
	return nil
}

// lookupChangedFiles calls the ChangedFilesClient (if wired) and
// returns (files, filterMode). filterMode is one of:
//
//   - "paths": the compare API succeeded; caller applies the
//     RootDir intersection.
//   - "full_fallback": any of {client nil, before empty, owner/
//     repo split failed, transport / 4xx / 5xx, ErrTruncated,
//     ErrBreakerOpen}. Caller falls back to the naive full
//     fan-out.
//
// Issue #432 phase 5 / ADR-050 §109: the local filterMode is
// always one of {paths, full_fallback} (a binary decision for
// the dispatcher's purposes). The granular per-error mode
// label (truncated / error / breaker_open) is incremented on
// the githubd_path_filter_total counter HERE — at the error
// site — so the §12 dashboard can distinguish the three
// fallback causes without forking the filterMode vocabulary
// into a third enum.
//
// Any error from the client is logged at warn level so the
// dashboard can group by mode + truncated flag.
func (s *Service) lookupChangedFiles(ctx context.Context, ev PushEvent, installationID int64) ([]string, string) {
	if s.ChangedFiles == nil {
		s.ObserveFilterMode(filterModeFullFallback)
		return nil, filterModeFullFallback
	}
	if ev.Before == "" {
		// First push on a branch (or stale webhook) — can't
		// form a compare URL. Treat as fallback.
		s.Log.Warn("githubd: push missing before SHA; falling back to full fan-out",
			"repo", ev.Repository.FullName, "sha", ev.After)
		s.ObserveFilterMode(filterModeFullFallback)
		return nil, filterModeFullFallback
	}
	owner, repo, ok := splitOwnerRepo(ev.Repository.FullName)
	if !ok {
		s.Log.Warn("githubd: invalid repo full name; falling back to full fan-out",
			"repo", ev.Repository.FullName, "sha", ev.After)
		s.ObserveFilterMode(filterModeFullFallback)
		return nil, filterModeFullFallback
	}
	files, err := s.ChangedFiles.ChangedFiles(ctx, installationID, owner, repo, ev.Before, ev.After)
	if err != nil {
		// Map the raw error to a granular metric label BEFORE
		// falling back. ErrBreakerOpen is incremented here
		// rather than in the breaker itself so the metric
		// reflects "this webhook saw the breaker open",
		// not "the breaker tripped" (the latter is an
		// internal state-change with no inbound webhook).
		switch {
		case errors.Is(err, ErrBreakerOpen):
			s.ObserveFilterModeWire(wire.PathFilterModeBreakerOpen)
		case errors.Is(err, ErrTruncated):
			s.ObserveFilterModeWire(wire.PathFilterModeTruncated)
		default:
			s.ObserveFilterModeWire(wire.PathFilterModeError)
		}
		s.Log.Warn("githubd: compare failed; falling back to full fan-out",
			"repo", ev.Repository.FullName, "base", ev.Before, "head", ev.After,
			"err", err, "truncated", errors.Is(err, ErrTruncated),
			"breaker_open", errors.Is(err, ErrBreakerOpen))
		return nil, filterModeFullFallback
	}
	s.ObserveFilterMode(filterModePaths)
	return files, filterModePaths
}

// ObserveFilterMode maps the local filterMode value to the
// wire.PathFilterMode* constant and increments the metric.
// The split (this helper + ObserveFilterModeWire) is so
// call sites that already hold a wire.* constant don't have
// to round-trip through the local filterMode vocabulary.
//
// Safe on nil Ops — the underlying accessor is nil-safe
// (the wire single-registry pattern documents this for every
// Observe* helper).
func (s *Service) ObserveFilterMode(local string) {
	switch local {
	case filterModePaths:
		s.Ops.ObserveGithubdPathFilter(wire.PathFilterModePaths)
	default:
		s.Ops.ObserveGithubdPathFilter(wire.PathFilterModeFullFallback)
	}
}

// ObserveFilterModeWire increments the metric with the wire
// label verbatim. Call sites that produce a granular mode
// (truncated / error / breaker_open) use this directly.
func (s *Service) ObserveFilterModeWire(mode string) {
	s.Ops.ObserveGithubdPathFilter(mode)
}

// filterByPath returns the subset of apps that should be rebuilt
// given the changed files. filterMode is one of:
//
//   - "paths": only apps whose RootDir intersects changedFiles,
//     plus all apps with RootDir == "" (they sit at repo root).
//     If NO app intersects the file set, rebuild ALL (spec: a
//     change at repo root outside every member's RootDir —
//     lockfile, root Dockerfile, CI config — rebuilds every
//     member). This is the lockfile/CI-fallback rule from
//     ADR-050 §103-109.
//   - "full_fallback": return touched verbatim; filter is a no-op.
//
// The returned `skipped` slice carries the IDs of touched apps
// the filter dropped on the "paths" path; it is empty on the
// "full_fallback" path.
func (s *Service) filterByPath(touched []state.App, changedFiles []string, filterMode string) ([]state.App, []string) {
	if filterMode != filterModePaths {
		return touched, nil
	}
	var matched []state.App
	var skipped []string
	anyMatched := false
	for _, app := range touched {
		if app.RootDir == "" {
			// Repo-root workload — always rebuild (it sees
			// every push).
			matched = append(matched, app)
			anyMatched = true
			continue
		}
		if pathIntersectsDir(changedFiles, app.RootDir) {
			matched = append(matched, app)
			anyMatched = true
		} else {
			skipped = append(skipped, app.ID)
		}
	}
	if !anyMatched {
		// Lockfile / root CI change: no member's RootDir was
		// touched → rebuild all per ADR-050 §103-109.
		return touched, nil
	}
	return matched, skipped
}

// pathIntersectsDir reports whether any changed file lives under
// the directory `dir`. `dir` must be repo-relative (no leading
// slash); `""` would be a repo-root workload and is filtered
// before this helper runs. A path equal to `dir` itself counts as
// intersecting (top-level file added at the root_dir path).
//
// Prefix collision guard: "services/auth" does NOT match
// "services/auth-api/x.ts" — the helper enforces a trailing
// slash boundary so the auth-dir-vs-auth-api-dir bug can't
// misclassify workloads.
func pathIntersectsDir(changedFiles []string, dir string) bool {
	prefix := dir + "/"
	for _, f := range changedFiles {
		// Three intersection shapes:
		//   f == dir       — top-level file added at the root_dir path
		//   f == dir + "/" — directory-level rename/add/remove with
		//                    trailing slash (GitHub emits these for
		//                    directory-only entries; without this
		//                    case, a workload whose RootDir matches
		//                    the renamed dir would be missed).
		//   f starts with prefix — file under the directory.
		if f == dir || f == prefix || strings.HasPrefix(f, prefix) {
			return true
		}
	}
	return false
}

// splitOwnerRepo splits "owner/name" into ("owner", "name", true).
// Returns ok=false if the input doesn't contain a single slash,
// or if either side is empty. Callers log + fall back.
func splitOwnerRepo(fullName string) (owner, repo string, ok bool) {
	idx := strings.Index(fullName, "/")
	if idx <= 0 || idx == len(fullName)-1 {
		return "", "", false
	}
	if strings.Contains(fullName[idx+1:], "/") {
		// More than one slash — owner/repo/extra. We accept
		// only the canonical two-segment shape.
		return "", "", false
	}
	return fullName[:idx], fullName[idx+1:], true
}

// ErrNoBinding is returned by HandlePushRequest when the push
// doesn't match any registered binding. The HTTP handler turns
// this into a 200 with an ignored-payload body.
var ErrNoBinding = errNoBinding{}

type errNoBinding struct{}

func (errNoBinding) Error() string { return "githubd: no binding for push" }

// IsNoBinding reports whether err is the no-binding sentinel.
func IsNoBinding(err error) bool {
	return errors.As(err, new(errNoBinding))
}

// ErrSkipDeploy is returned when a push contains an explicit deployment
// opt-out marker ([skip deploy] or [deploy skip]). The HTTP handler maps it to
// a successful ignored response and the durable worker completes the inbox row
// without retrying the delivery.
var ErrSkipDeploy = errSkipDeploy{}

type errSkipDeploy struct{}

func (errSkipDeploy) Error() string { return "githubd: deployment skipped by commit marker" }

func IsSkipDeploy(err error) bool {
	return errors.As(err, new(errSkipDeploy))
}

// previewDefaultTTL is the wall-clock duration a preview app
// stays alive after provision time, regardless of GitHub state.
// Mirrors the customer-facing docstring on
// state.App.PreviewExpiresAt — the teardown janitor (PR-C)
// reaps after this deadline. The 7-day default matches ADR-094
// §3.4; per-account overrides arrive in a follow-up ADR.
const previewDefaultTTL = 7 * 24 * time.Hour

// previewHostnameForSlug derives the customer-facing preview URL
// from the preview app slug. Shape:
//
//	pr-{N}-{parent_slug}  →  pr-{N}-{parent_slug}.gregale.dev
//
// The wildcard DNS record *.gregale.dev + the wildcard TLS
// cert already cover this hostname (deploy/terraform/wildcard_*);
// PR-B wires the gateway-internal parser that peels the
// pr-{N}- prefix back off the slug. Returns "" for empty slugs
// so the caller can pass the slug through without a guard.
func previewHostnameForSlug(slug string) string {
	if slug == "" {
		return ""
	}
	domain := strings.Trim(strings.TrimSpace(os.Getenv("FAAS_APPS_DOMAIN")), ".")
	if domain == "" {
		domain = "gregale.dev"
	}
	return slug + "." + domain
}

// dashboardDestroyPreviewURL derives the customer-facing dashboard
// one-click destroy URL from the parent app's slug + the preview
// slug (issue #961 Mega-C PR-1, leaf 3). Shape:
//
//	/dashboard/apps/{parent_slug}/preview/{preview_slug}/destroy
//
// The route is mounted by cmd/apid/server.go as
// POST /dashboard/apps/{slug}/preview/{previewSlug}/destroy (a
// future PR-2 commit); githubd's job is to compose the URL and
// embed it in the PR-thread comment Markdown so the author can
// click through to the dashboard action.
func dashboardDestroyPreviewURL(parentSlug, previewSlug string) string {
	if parentSlug == "" || previewSlug == "" {
		return ""
	}
	domain := strings.Trim(strings.TrimSpace(os.Getenv("FAAS_APPS_DOMAIN")), ".")
	if domain == "" {
		domain = "gregale.dev"
	}
	return "https://" + domain + "/dashboard/apps/" + parentSlug + "/preview/" + previewSlug + "/destroy"
}

// previewCommentOnce stamps apps.preview_destroy_commented_at
// (the dedupe carrier from migration 00296) and returns true
// only on the first call per preview row. Subsequent calls
// return false so the caller can short-circuit the GitHub POST.
//
// The stamp runs BEFORE the POST so a duplicate enqueue (e.g.
// PR close + reopen within the same webhook burst) collapses
// to a single comment. A failed POST does NOT clear the stamp
// — the customer's intent ("don't spam my PR thread") is
// already satisfied by the dedupe, and a re-attempt on a later
// trigger can refresh the comment.
//
// Returns false (no-op) when the Store seam is missing — the
// close-arm should never block on dedupe state.
func (s *Service) previewCommentOnce(ctx context.Context, appID string) (firstTime bool, stampErr error) {
	if s.Reconcile == nil || s.Reconcile.Store == nil {
		return false, nil
	}
	current, err := s.Reconcile.Store.AppByID(ctx, appID)
	if err != nil {
		// Best-effort dedupe: an unreadable AppByID must NOT
		// block the close-arm — the customer's intent ("don't
		// spam my PR thread") is already satisfied by the
		// Store.StampPreviewDestroyCommentedAt being a no-op
		// when the row is missing, and a re-attempt on a later
		// trigger can refresh the comment.
		//nolint:nilerr
		return false, nil
	}
	if current.PreviewDestroyCommentedAt != nil {
		return false, nil
	}
	if _, err := s.Reconcile.Store.StampPreviewDestroyCommentedAt(ctx, appID, time.Now().UTC()); err != nil {
		return false, err
	}
	return true, nil
}

// previewSlug derives the slug githubd uses for a preview app
// row from the parent app's slug + the PR number (issue #272 /
// ADR-094 D2). The shape is `pr-{N}-{parent_slug}`, e.g. a
// PR #42 against the `hello-world` app lands at
// `pr-42-hello-world`. Stable across synchronize / reopened
// events so a 2nd push to PR #42 reuses the same row.
//
// Returns an error when parentSlug is empty (the binding
// resolution must have surfaced a non-empty parent — an empty
// parent indicates a githubd-side bug, not a customer-facing
// failure mode).
func previewSlug(parentSlug string, prNumber int) (string, error) {
	if parentSlug == "" {
		return "", fmt.Errorf("githubd: previewSlug: parent_slug is empty (binding resolution bug)")
	}
	if prNumber <= 0 {
		return "", fmt.Errorf("githubd: previewSlug: pr_number must be > 0 (got %d)", prNumber)
	}
	return fmt.Sprintf("pr-%d-%s", prNumber, parentSlug), nil
}

// stampPreviewPrState (ADR-095 PR-C) advances one preview row's
// lifecycle label via SetPreviewPrState. It exists as a Service
// method so the dispatcher's helper stays above the
// Handler ≤ 50-line cap and the test rig (newPreviewService) can
// stub it when an action's handler should not exercise the store.
//
// The implementation is a thin delegate — the closed-set
// validator and the preview-only enforcement live inside
// SetPreviewPrState, so a bug here can never relabel a
// production app or trip the CHECK constraint. Returns nil when
// the store declines (production row, ErrNotFound) — the
// dispatcher has already passed the preview-path gate, so
// ErrNotFound means a concurrent delete raced us; in that case
// the row is gone and there's nothing to stamp.
func (s *Service) stampPreviewPrState(ctx context.Context, appID, prState string) error {
	if s.Reconcile == nil || s.Reconcile.Store == nil {
		return nil
	}
	updated, err := s.Reconcile.Store.SetPreviewPrState(ctx, appID, prState)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return nil
		}
		return err
	}
	_ = updated
	return nil
}

// WritePreviewCheck is the seam HandlePullRequest uses for the
// queued / building / live preview Check Run. Wired by
// cmd/githubd/main.go to a *ChecksAPI.WritePreviewCheck;
// nil-safe (the dispatcher logs + proceeds when nil,
// mirroring the production WriteCheck seam on Service).
//
// The func-typed shape (rather than a method on Service)
// matches the existing WriteCheck seam on line 71 and lets
// the test path inject a recording func without constructing
// a full ChecksAPI.
type WritePreviewCheckFunc func(ctx context.Context, repoFullName, commitSHA string, phase githubdgrpc.CheckPhase, previewURL, summary string) error

type WritePreviewCheckForInstallationFunc func(ctx context.Context, installationID int64, repoFullName, commitSHA string, phase githubdgrpc.CheckPhase, previewURL, summary string) error

// WritePreviewCheckForkRefusedFunc is the seam HandlePullRequest
// uses for the D3 fork-refused neutral Check Run. Same func-
// typed shape as WritePreviewCheckFunc.
type WritePreviewCheckForkRefusedFunc func(ctx context.Context, repoFullName, commitSHA, summary string) error

type WritePreviewCheckForkRefusedForInstallationFunc func(ctx context.Context, installationID int64, repoFullName, commitSHA, summary string) error

func (s *Service) writePreviewCheck(ctx context.Context, installationID int64, repoFullName, commitSHA string, phase githubdgrpc.CheckPhase, previewURL, summary string) error {
	if s.WritePreviewCheckForInstallation != nil {
		return s.WritePreviewCheckForInstallation(ctx, installationID, repoFullName, commitSHA, phase, previewURL, summary)
	}
	if s.WritePreviewCheck != nil {
		return s.WritePreviewCheck(ctx, repoFullName, commitSHA, phase, previewURL, summary)
	}
	return nil
}

func (s *Service) writeForkRefusedCheck(ctx context.Context, installationID int64, repoFullName, commitSHA, summary string) error {
	if s.WritePreviewCheckForkRefusedForInstallation != nil {
		return s.WritePreviewCheckForkRefusedForInstallation(ctx, installationID, repoFullName, commitSHA, summary)
	}
	if s.WritePreviewCheckForkRefused != nil {
		return s.WritePreviewCheckForkRefused(ctx, repoFullName, commitSHA, summary)
	}
	return nil
}

// WritePreviewDestroyCommentFunc is the seam HandlePullRequest
// uses for the one-time PR-comment destroy hint (issue #961
// Mega-C PR-1, leaf 3). Body is a Markdown fragment; the caller
// composes the dashboard URL prefix.
type WritePreviewDestroyCommentFunc func(ctx context.Context, repoFullName string, prNumber int, body string) error

// WritePreviewCommentForInstallationFunc is the installation-scoped seam for
// the single updatable PR preview comment. marker is a hidden, stable token
// used by the GitHub writer to find the existing comment on retries.
type WritePreviewCommentForInstallationFunc func(ctx context.Context, installationID int64, repoFullName string, prNumber int, marker, body string) error

func previewCommentMarker(previewSlug string) string {
	return "<!-- gregale-preview:" + previewSlug + " -->"
}

func previewCommentBody(marker, previewSlug, previewURL, parentSlug, commitSHA string, closed bool) string {
	status := "queued"
	statusCopy := "The preview build is queued."
	if closed {
		status = "closed"
		statusCopy = "The PR is closed; teardown is scheduled after the grace period."
	}
	destroyURL := dashboardDestroyPreviewURL(parentSlug, previewSlug)
	return fmt.Sprintf("%s\n### Gregale preview — %s\n\n%s\n\n[Open preview](%s) · [Destroy preview](%s)\n\nCommit: `%s`", marker, status, statusCopy, previewURL, destroyURL, commitSHA)
}

func (s *Service) writePreviewComment(ctx context.Context, installationID int64, repoFullName string, prNumber int, marker, body string) error {
	if s.WritePreviewCommentForInstallation == nil {
		return nil
	}
	return s.WritePreviewCommentForInstallation(ctx, installationID, repoFullName, prNumber, marker, body)
}

// handlePullRequest is the HTTP webhook entry point for the
// pull_request event family (issue #272 / ADR-094). It provisions
// a preview app row, stages and enqueues its source build, and updates a
// `gregale-preview` Check Run through the deployment lifecycle.
//
// Differences from HandlePushRequest:
//
//   - No reconcile / scan / build-fan-out: PR previews deploy
//     exactly one app (the preview itself) per event.
//   - D3 fork refusal short-circuits BEFORE any app creation —
//     the policy is uniform-refuse and the neutral Check Run is
//     the only outbound signal.
//   - The action whitelist (opened/synchronize/reopened/closed)
//     is enforced by DecodePullRequest; an unknown action
//     surfaces as a parse error here.
//   - The (repo, branch) binding resolution used by the push
//     path is replaced by a (repo) lookup — PRs land on the
//     binding's parent app regardless of which branch the PR
//     targets.
//
// Returns:
//
//   - reconcile.Result on success (Added carries the preview
//     app row when one was provisioned).
//   - ErrNoBinding when the base repo isn't bound (mirrors
//     HandlePushRequest — the HTTP handler renders the same
//     200-ignored body so GitHub doesn't retry).
//   - ErrIgnored when the PR is a fork (D3) or when the
//     account's quota is exhausted. The HTTP handler renders
//     the same 200-ignored body.
//   - any other error → 500 (logged with op context).
func (s *Service) handlePullRequest(ctx context.Context, body []byte) (reconcile.Result, error) {
	ev, err := DecodePullRequest(body)
	if err != nil {
		return reconcile.Result{}, err
	}

	// D3 fork refusal: short-circuit BEFORE any app creation.
	// The neutral Check Run is the only outbound signal — no
	// apps row, no deployment row, no Slack/audit notifications.
	if ev.IsFork() {
		if werr := s.writeForkRefusedCheck(ctx, ev.Installation.ID,
			ev.Repository.FullName, ev.PullRequest.HeadSHA,
			"Fork PR refused — head repo differs from base repo "+
				"(security policy; ADR-094 D3)"); werr != nil {
			s.Log.Warn("githubd: write fork-refused preview check", "err", werr,
				"repo", ev.Repository.FullName, "sha", ev.PullRequest.HeadSHA)
		}
		s.Log.Info("githubd pull_request: fork refused",
			"repo", ev.Repository.FullName,
			"head_repo", ev.HeadRepoFullName(),
			"pr_number", ev.Number, "action", string(ev.Action),
			"sender", ev.Sender.Login)
		result := reconcile.Result{}
		result.WasIgnored = true
		return result, ErrIgnored
	}

	// 1. Resolve the (repo) binding. PR previews don't branch-
	//    filter: any PR opened against a bound repo triggers
	//    preview provisioning for the parent app. The empty-
	//    branch argument is the canonical "match any branch"
	//    shape for AppBindingStore.GetAppBinding.
	binding, err := appBindingForEvent(ctx, s.Bindings, ev.Repository.FullName, "", ev.Installation.ID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) || IsNoBinding(err) {
			return reconcile.Result{}, ErrNoBinding
		}
		return reconcile.Result{}, fmt.Errorf("githubd: resolve binding for PR: %w", err)
	}
	if binding.BindingID == "" || binding.AccountID == "" || binding.AppID == "" {
		// Defensive: the bind row exists but is incomplete.
		// The OAuth handshake should never write a partial row,
		// but a manual SQL edit could.
		return reconcile.Result{}, ErrNoBinding
	}
	if ev.Installation.ID > 0 && binding.InstallID != ev.Installation.ID {
		return reconcile.Result{}, ErrNoBinding
	}

	// 2. Resolve the install row (same shape as HandlePushRequest).
	install, err := installForAccount(ctx, s.Installs, binding.AccountID, binding.InstallID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return reconcile.Result{}, ErrNoBinding
		}
		return reconcile.Result{}, fmt.Errorf("githubd: resolve install for PR: %w", err)
	}
	if install.AccountID == "" || install.InstallationID == 0 {
		return reconcile.Result{}, ErrNoBinding
	}

	// 3. Takeover guard: the bind row's InstallID must match
	//    the install row's InstallationID. Same §11 fail-closed
	//    posture as HandlePushRequest.
	if binding.InstallID != install.InstallationID {
		s.Log.Warn("githubd: PR binding install_id mismatch",
			"binding_id", binding.BindingID,
			"binding_install_id", binding.InstallID,
			"install_installation_id", install.InstallationID,
			"repo", ev.Repository.FullName, "pr_number", ev.Number)
		return reconcile.Result{}, ErrNoBinding
	}

	// 4. Load the parent app. The binding's AppID is the parent;
	//    the preview row's slug is `pr-{N}-{parent_slug}` and its
	//    preview_of_slug column carries the parent's slug verbatim.
	parentApp, err := s.Reconcile.Store.AppByID(ctx, binding.AppID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			// Bind row points at a deleted parent — treat as
			// unbound. The dashboard's "orphaned bindings"
			// pane surfaces this case to operators.
			return reconcile.Result{}, ErrNoBinding
		}
		return reconcile.Result{}, fmt.Errorf("githubd: resolve parent app: %w", err)
	}
	if parentApp.Status != state.AppActive {
		// The parent isn't active (soft-deleted, suspended, etc.).
		// Provisioning a preview on top of an inactive parent is
		// nonsensical — refuse silently so GitHub doesn't retry.
		return reconcile.Result{}, ErrNoBinding
	}

	// 5. Derive the preview slug + provision the preview apps row.
	//    Idempotent on (account_id, slug) — a 2nd synchronize
	//    event for the same PR reuses the row, updates the
	//    preview_pr_state / preview_expires_at, and re-stamps
	//    the Check Run.
	previewSlugVal, err := previewSlug(parentApp.Slug, ev.Number)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("githubd: derive preview slug: %w", err)
	}
	if ev.Action == PullRequestActionClosed {
		if _, lookupErr := s.Reconcile.Store.AppBySlug(ctx, previewSlugVal); lookupErr != nil {
			if errors.Is(lookupErr, state.ErrNotFound) {
				// GitHub can deliver a close after retention already removed the
				// preview (or without a prior open). Teardown is an idempotent no-op;
				// never create a quota-consuming closed app just to delete it later.
				return reconcile.Result{}, nil
			}
			return reconcile.Result{}, fmt.Errorf("githubd: resolve closing preview: %w", lookupErr)
		}
	}

	// Closed-event teardown is dispatched to PR-C's janitor —
	// the dispatcher just stamps preview_pr_state='closed' on
	// the row and the janitor does the rest (24h grace + final
	// torn_down). Opening this up to the dispatcher would
	// duplicate the janitor's state machine.
	previewState := state.PreviewPrStateOpen
	if ev.Action == PullRequestActionClosed {
		previewState = state.PreviewPrStateClosed
	}

	expiresAt := time.Now().Add(previewDefaultTTL)
	previewApp := state.App{
		// ID is left blank — pgstore.CreateAppIfUnderQuota mints a
		// UUIDv7 when App.ID is empty. The preview app is a fresh
		// row; reusing the parent's ID would collide on the PK.
		AccountID:        parentApp.AccountID,
		Slug:             previewSlugVal,
		Type:             parentApp.Type,
		Runtime:          parentApp.Runtime,
		RAMMB:            parentApp.RAMMB,
		MaxConcurrency:   parentApp.MaxConcurrency,
		IdleTimeoutS:     parentApp.IdleTimeoutS,
		RootDir:          parentApp.RootDir,
		WorkloadClass:    parentApp.WorkloadClass,
		StartCommand:     parentApp.StartCommand,
		Manifest:         parentApp.Manifest,
		AppProtocol:      parentApp.AppProtocol,
		Status:           state.AppActive,
		PreviewOfSlug:    parentApp.Slug,
		PreviewPrNumber:  ev.Number,
		PreviewPrState:   previewState,
		PreviewExpiresAt: &expiresAt,
	}
	// ADR-094 D4: previews are apps and consume the account's real plan quota.
	// Resolve the account server-side instead of using a synthetic high ceiling
	// that lets webhook traffic bypass the customer-facing quota boundary.
	account, err := s.Reconcile.Store.AccountByID(ctx, binding.AccountID)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("githubd: resolve preview account limits: %w", err)
	}
	previewLimits := api.MustLimitsFor(account.Plan)
	created, err := s.Reconcile.Store.CreateAppIfUnderQuota(ctx, previewApp, previewLimits)
	if err != nil {
		var qe *state.QuotaError
		if errors.As(err, &qe) {
			// Rebuilding an existing preview does not consume another slot. The
			// quota guard runs before the uniqueness check, so resolve that
			// idempotent path before reporting the account as full.
			existing, lookupErr := s.Reconcile.Store.AppBySlug(ctx, previewSlugVal)
			if lookupErr == nil && existing.AccountID == parentApp.AccountID &&
				existing.PreviewOfSlug == parentApp.Slug {
				created = existing
				err = nil
			} else {
				previewURL := "https://" + previewHostnameForSlug(previewSlugVal)
				if werr := s.writePreviewCheck(ctx, install.InstallationID,
					ev.Repository.FullName, ev.PullRequest.HeadSHA,
					githubdgrpc.CheckPhaseFailed, previewURL,
					"Preview skipped: account has reached its deployed app limit. "+
						"Close an existing app or upgrade your plan."); werr != nil {
					s.Log.Warn("githubd: write quota preview check", "err", werr,
						"repo", ev.Repository.FullName, "sha", ev.PullRequest.HeadSHA)
				}
				s.Log.Info("githubd pull_request: quota exhausted",
					"repo", ev.Repository.FullName, "pr_number", ev.Number,
					"sender", ev.Sender.Login)
				result := reconcile.Result{}
				result.WasIgnored = true
				return result, ErrIgnored
			}
		}
	}
	if err != nil {
		// Pre-existing row on (account_id, slug) → ErrConflict.
		// That's the idempotent path: a 2nd synchronize /
		// reopened / closed event for the same PR. We treat it as
		// success — the row is already provisioned. The state
		// machine still has to advance, though, so the dispatcher
		// looks the existing row up by slug (the freshly-failed
		// INSERT didn't mint an ID) and stamps preview_pr_state
		// on it. Before PR-C this branch swallowed ErrConflict
		// silently, which meant a `closed` event never actually
		// mutated the row's preview_pr_state — the janitor's
		// only signal was preview_expires_at, so PRs that were
		// reopened-then-closed stayed open forever.
		// ADR-095 PR-C.1.
		if !errors.Is(err, state.ErrConflict) {
			return reconcile.Result{}, fmt.Errorf("githubd: create preview app: %w", err)
		}
		existing, lookupErr := s.Reconcile.Store.AppBySlug(ctx, previewSlugVal)
		if lookupErr != nil {
			// The row vanished between the ErrConflict and the
			// lookup — treat it as a teardown race: the row is
			// already gone, so there's nothing to stamp and
			// nothing for the janitor to reap. Return success
			// so GitHub doesn't retry; the Check Run we'll
			// write below reflects the live preview URL, which
			// is fine because the row's deletion makes it 410.
			s.Log.Info("githubd pull_request: conflict path lost row to concurrent delete",
				"err", lookupErr, "preview_slug", previewSlugVal)
			return reconcile.Result{}, nil
		}
		created = existing
	}

	// 5b. Stamp preview_pr_state on the existing row. This covers
	//     two paths the INSERT above can't: (a) the
	//     opened-on-already-existing-row case (ErrConflict from
	//     step 5), and (b) the explicit closed-action teardown
	//     arm — even when the row was first provisioned earlier
	//     in the PR's lifetime, we now flip the label so the
	//     janitor's 24h grace clock starts. SetPreviewPrState
	//     refuses production rows and out-of-vocabulary values,
	//     so this UPDATE cannot relabel a customer's live app
	//     or trip the CHECK constraint.
	if err := s.stampPreviewPrState(ctx, created.ID, previewState); err != nil {
		s.Log.Warn("githubd: stamp preview_pr_state",
			"err", err, "app_id", created.ID, "state", previewState)
	}

	result := reconcile.Result{Added: []state.App{created}}

	// 5c. Upsert the stable PR status comment. Older deployments that
	//     only wire WritePreviewDestroyComment retain the original
	//     close-only destroy hint as a compatibility fallback.
	previewURL := "https://" + previewHostnameForSlug(previewSlugVal)
	if s.WritePreviewCommentForInstallation != nil {
		marker := previewCommentMarker(previewSlugVal)
		body := previewCommentBody(marker, previewSlugVal, previewURL, parentApp.Slug,
			ev.PullRequest.HeadSHA, ev.Action == PullRequestActionClosed)
		if werr := s.writePreviewComment(ctx, install.InstallationID, ev.Repository.FullName,
			ev.Number, marker, body); werr != nil {
			s.Log.Warn("githubd: upsert preview status comment", "err", werr,
				"repo", ev.Repository.FullName, "pr", ev.Number,
				"preview_slug", previewSlugVal)
		}
	}
	// 5d. On a closed PR, post a one-time destroy-hint comment
	//     on the PR thread (issue #961 Mega-C PR-1, leaf 3).
	//     previewCommentOnce dedupes via the new
	//     apps.preview_destroy_commented_at column so close →
	//     reopen → close cycles do not spam the PR thread with
	//     duplicate comments. The seam is nil-safe — when
	//     githubd's main.go doesn't wire a writer (e.g. tests,
	//     or a deployment where we want to disable the surface
	//     temporarily), this block short-circuits cleanly.
	if ev.Action == PullRequestActionClosed && s.WritePreviewCommentForInstallation == nil && s.WritePreviewDestroyComment != nil {
		firstTime, _ := s.previewCommentOnce(ctx, created.ID)
		if firstTime {
			destroyURL := dashboardDestroyPreviewURL(parentApp.Slug, previewSlugVal)
			body := fmt.Sprintf(
				"Preview `%s` is open against `%s`. [Tear it down from the dashboard](%s).",
				previewSlugVal, parentApp.Slug, destroyURL)
			if werr := s.WritePreviewDestroyComment(ctx,
				ev.Repository.FullName, ev.Number, body); werr != nil {
				s.Log.Warn("githubd: write preview destroy comment", "err", werr,
					"repo", ev.Repository.FullName, "pr", ev.Number,
					"preview_slug", previewSlugVal)
			}
		}
	}
	if ev.Action == PullRequestActionClosed {
		// Closing a PR advances only the preview lifecycle. It must not enqueue a
		// fresh build for a revision that is being torn down.
		return result, nil
	}

	// 6. Write the queued Check Run with the preview URL. The
	//    URL is derived from the preview slug; routing (PR-B)
	//    peels the prefix back off. Posting `status=queued` so
	//    the PR UI shows the spinner; the subsequent build
	//    pipeline (a follow-up PR-A.1) will transition it to
	//    `in_progress` / `completed`.
	if webhookDeliveryID(ctx) == "" {
		if werr := s.writePreviewCheck(ctx, install.InstallationID,
			ev.Repository.FullName, ev.PullRequest.HeadSHA,
			githubdgrpc.CheckPhaseQueued, previewURL,
			fmt.Sprintf("Preview provisioned for PR #%d against %q",
				ev.Number, parentApp.Slug)); werr != nil {
			s.Log.Warn("githubd: write queued preview check", "err", werr,
				"repo", ev.Repository.FullName, "sha", ev.PullRequest.HeadSHA,
				"preview_slug", previewSlugVal)
		}
	}

	// Fetch, stage, and enqueue the preview's head revision. Older unit rigs
	// intentionally omit these production dependencies; production always wires
	// all three and therefore turns every open/synchronize/reopen event into a
	// real DeploymentKindPreview build.
	if s.Source != nil && s.Enqueuer != nil && s.WorkDir != "" {
		tree, fetchErr := s.Source.Fetch(ctx, binding.AccountID, install.InstallationID,
			ev.Repository.FullName, ev.PullRequest.HeadSHA)
		if fetchErr != nil {
			return result, fmt.Errorf("githubd: fetch preview source: %w", fetchErr)
		}
		defer func() { _ = tree.Close() }()
		project := state.Project{
			AccountID:        binding.AccountID,
			RepoFullName:     ev.Repository.FullName,
			ProductionBranch: ev.PullRequest.HeadRef,
		}
		sourcePath, sourceBytes, sourceURL, stageErr := s.stageAppSource(ctx, tree, created,
			project, ev.PullRequest.HeadSHA, ev.PullRequest.HeadRef)
		if stageErr != nil {
			return result, fmt.Errorf("githubd: stage preview source: %w", stageErr)
		}
		build, enqueueErr := s.Enqueuer.Enqueue(ctx, BuildSpec{
			App:          created,
			DeliveryID:   webhookDeliveryID(ctx),
			CommitSHA:    ev.PullRequest.HeadSHA,
			RepoFullName: ev.Repository.FullName,
			Ref:          "refs/heads/" + ev.PullRequest.HeadRef,
			Branch:       ev.PullRequest.HeadRef,
			Pusher:       ev.Sender.Login,
			SourcePath:   sourcePath,
			SourceURL:    sourceURL,
			SourceBytes:  sourceBytes,
			PRNumber:     int32(ev.Number),
			SenderLogin:  ev.Sender.Login,
			EventKind:    githubdpb.EnqueueBuildEventKind_EVENT_KIND_PULL_REQUEST,
		})
		if enqueueErr != nil {
			return result, fmt.Errorf("githubd: enqueue preview build: %w", enqueueErr)
		}
		result.BuildIDs = []string{build.ID}
	}

	s.Log.Info("githubd pull_request → preview",
		"repo", ev.Repository.FullName,
		"pr_number", ev.Number, "action", string(ev.Action),
		"preview_slug", previewSlugVal,
		"parent_slug", parentApp.Slug,
		"sender", ev.Sender.Login,
		"sha", ev.PullRequest.HeadSHA)

	return result, nil
}

// ErrIgnored is returned by HandlePushRequest when the pushed
// branch is not the project's production branch and the guard
// short-circuited reconcile. The HTTP handler turns this into a
// 200 with {status:ignored, reason:feature_branch} so GitHub does
// not retry.
var ErrIgnored = errIgnored{}

type errIgnored struct{}

func (errIgnored) Error() string { return "githubd: push to non-production branch" }

// IsIgnored reports whether err is the ignored sentinel.
func IsIgnored(err error) bool {
	return errors.As(err, new(errIgnored))
}

// refToBranch converts "refs/heads/main" → "main". Returns "" for
// refs that aren't a branch.
func refToBranch(ref string) string {
	const prefix = "refs/heads/"
	if len(ref) <= len(prefix) {
		return ""
	}
	if ref[:len(prefix)] != prefix {
		return ""
	}
	return ref[len(prefix):]
}

// refToTag converts "refs/tags/v1.0.0" → "v1.0.0". GitHub sends
// tag creation, update, and deletion through the same push webhook
// shape as branch pushes. The caller decides whether a tag event is
// deployable; an empty result means the ref is not a tag.
func refToTag(ref string) string {
	const prefix = "refs/tags/"
	if len(ref) <= len(prefix) || !strings.HasPrefix(ref, prefix) {
		return ""
	}
	return ref[len(prefix):]
}

// WebhookHTTPHandler returns an http.Handler that serves
// POST /webhooks/github. Today it returns 503 because the proxy
// (cmd/gatewayd-internal) verifies the signature and forwards; this handler
// is loopback-only and reachable from the gatewayd-internal reverse proxy.
// A future PR may let githubd stand up its own listener when
// gatewayd-internal isn't on the same host (not in v1.0).
func (s *Service) WebhookHTTPHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "githubd: webhook arrives via gatewayd-internal's edge-verifying proxy", http.StatusNotImplemented)
	})
}
