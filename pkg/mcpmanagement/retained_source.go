package mcpmanagement

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Historical provenance selects a commit, never an arbitrary fetch URL or a
// branch. Current installation access and target intent are reviewed again.
func (m *management) attachRetainedSource(ctx context.Context, p *releasePlan, d api.DeploymentResponse) error {
	if len(d.SourceURL) > 512 || !sourceCommit.MatchString(d.CommitSHA) || !strings.HasPrefix(d.SourceURL, "github://") {
		return invalid("invalid_target", "retained source requires canonical GitHub commit provenance")
	}
	repo, commit, ok := strings.Cut(strings.TrimPrefix(d.SourceURL, "github://"), "@")
	if !ok || !sourceRepository.MatchString(repo) || len(repo) > 200 || commit != d.CommitSHA {
		return invalid("invalid_target", "retained source requires an exact repository and commit; branch and upload provenance cannot be rebuilt")
	}
	environment := ""
	if p.EnvironmentID != "" {
		environment = p.Scope
	}
	source, err := m.client.ReviewSourceRef(ctx, p.App, api.SourceReviewRequest{Repo: repo, Ref: commit, Environment: environment})
	if retainedSourceNeedsInstallation(err) {
		// A public repository deployed without an installation
		// (ADR-20261126164847) has no reviewable access to rebuild through.
		return invalid("invalid_target", "retained source rebuilds need a GitHub installation that can see "+repo+
			"; a commit deployed from a public repository without one can be deployed again from source instead")
	}
	if err != nil {
		return err
	}
	if source.AppID != p.AppID || !strings.EqualFold(source.Repo, repo) || source.CommitSHA != commit || source.RepositoryID <= 0 || source.InstallationID <= 0 || len(source.SourceRoot) > 1024 || source.SourceRoot != d.SourceRoot || !reflect.DeepEqual(source.ReleaseReview, *p.review) {
		return invalid("plan_stale", "retained commit, repository access, build root or current target review changed; inspect before rebuilding")
	}
	p.Source, p.RequestedRef = &source, commit
	p.ExpiresAt = time.Now().Add(releasePlanTTL + time.Duration(api.BuildTimeoutSeconds)*time.Second)
	return nil
}

// retainedSourceNeedsInstallation reports a source review refused because no
// installation of the account sees the repository.
func retainedSourceNeedsInstallation(err error) bool {
	var apiErr *api.APIError
	return errors.As(err, &apiErr) && (apiErr.Problem.Code == api.CodeGitHubInstallNotFound || apiErr.Problem.Code == api.CodeGitHubRepoNotAccessible)
}
