package githubd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/state"
)

// previewSetupError is a PR preview failure a retried delivery cannot fix:
// the PR head, the plan or the account must change first. Its reason is
// written to the pull request, which can be public, so it carries only what
// the repository or the account owner already knows.
type previewSetupError struct{ reason string }

func (e *previewSetupError) Error() string { return "githubd: preview setup: " + e.reason }

func previewSetupFailure(format string, args ...any) error {
	return &previewSetupError{reason: fmt.Sprintf(format, args...)}
}

// previewQuotaSummary explains a PR preview admission failure. workloads is
// the size of the set the PR needs.
func previewQuotaSummary(quota *state.QuotaError, workloads int) string {
	if quota.Kind == state.QuotaErrorKindPreviewWorkloads {
		return fmt.Sprintf("this pull request's preview needs %d workloads and the plan allows %d in one PR preview environment. "+
			"Upgrade the plan, or set `service_policy: declared` in .gregale/project.yaml so only declared dependencies join the preview.",
			workloads, quota.Limit)
	}
	return fmt.Sprintf("the plan allows %d open PR preview environment(s) and all are in use. "+
		"Close a pull request, run `gregale preview destroy` on one of its previews, or upgrade the plan.", quota.Limit)
}

// enqueueSetupFailure classifies an apid EnqueueBuild rejection. Rejections
// a retry cannot clear inside the delivery's retry budget become setup
// failures; anything else, including apid's internal validation of githubd's
// own staging, stays a retryable error. Account states are summarised:
// billing or abuse holds must not appear on a public pull request.
func enqueueSetupFailure(workload string, err error) error {
	var rejected interface{ GRPCStatus() *status.Status }
	if !errors.As(err, &rejected) || rejected.GRPCStatus() == nil {
		return nil
	}
	st := rejected.GRPCStatus()
	message := strings.TrimPrefix(st.Message(), "EnqueueBuild: ")
	switch st.Code() {
	case codes.ResourceExhausted:
		return previewSetupFailure("%s could not be built: %s", workload, message)
	case codes.InvalidArgument:
		for _, customerFile := range []string{"source manifest: ", "release command: "} {
			if strings.HasPrefix(message, customerFile) {
				return previewSetupFailure("%s could not be built: %s", workload, message)
			}
		}
	case codes.FailedPrecondition:
		return previewSetupFailure("the Gregale account cannot deploy right now; open the Gregale dashboard for details")
	}
	return nil
}

// reportPreviewSetupFailure records the failure on the PR's preview set and
// writes it to the PR's gregale-preview check and status comment. Recording
// moves the set to this head, so the previous head's deployments can no
// longer turn the check green.
func (s *Service) reportPreviewSetupFailure(ctx context.Context, ev PullRequestEvent, target pullRequestTarget, reason string) {
	slug, err := previewSlug(target.parent.Slug, ev.Number)
	if err != nil {
		return
	}
	head := state.PRPreviewHead{InstallationID: target.install.InstallationID, RepoFullName: ev.Repository.FullName,
		PRNumber: ev.Number, CommitSHA: ev.PullRequest.HeadSHA}
	if recorder, ok := s.Reconcile.Store.(state.PRPreviewSetupFailureStore); ok {
		if err := recorder.FailPRPreviewSetup(ctx, head, reason); err != nil && !errors.Is(err, state.ErrNotFound) {
			s.Log.Warn("githubd: record preview setup failure", "err", err,
				"repo", head.RepoFullName, "pr", head.PRNumber)
		}
	}
	previewURL := "https://" + previewHostnameForSlug(slug)
	if err := s.writePreviewCheck(ctx, head.InstallationID, head.RepoFullName, head.CommitSHA,
		githubdgrpc.CheckPhaseFailed, previewURL, "Preview failed: "+reason); err != nil {
		s.Log.Warn("githubd: write failed preview check", "err", err,
			"repo", head.RepoFullName, "sha", head.CommitSHA)
	}
	marker := previewCommentMarker(slug)
	body := previewCommentBody(marker, slug, previewURL, head.CommitSHA, "failed", reason)
	if err := s.writePreviewComment(ctx, head.InstallationID, head.RepoFullName, head.PRNumber, marker, body); err != nil {
		s.Log.Warn("githubd: upsert failed preview comment", "err", err,
			"repo", head.RepoFullName, "pr", head.PRNumber)
	}
	s.Log.Info("githubd pull_request: preview setup failed", "repo", head.RepoFullName,
		"pr_number", head.PRNumber, "sha", head.CommitSHA, "reason", reason)
}

// previewDeliveryExhausted is written to a PR whose delivery kept failing
// with retryable errors until the worker gave up.
const previewDeliveryExhausted = "Gregale could not set up this preview after repeated attempts. Push a new commit to retry; if it keeps failing, contact support."

// ReportUndeliverablePullRequest tells a pull request that its preview
// delivery exhausted its retries. Without it, the PR's comment would keep
// saying "queued" and no check would ever report. A closed, superseded,
// fork or unbound delivery is left alone.
func (s *Service) ReportUndeliverablePullRequest(ctx context.Context, delivery WebhookDelivery) {
	if delivery.EventType != "pull_request" {
		return
	}
	ev, err := DecodePullRequest(delivery.Payload)
	if err != nil || ev.Action == PullRequestActionClosed || ev.IsFork() {
		return
	}
	target, err := s.resolvePullRequestTarget(ctx, ev)
	if err != nil {
		return
	}
	if current, err := s.currentPullRequestDelivery(ctx, target.install.InstallationID, ev); err != nil || !current {
		return
	}
	s.reportPreviewSetupFailure(ctx, ev, target, previewDeliveryExhausted)
}
