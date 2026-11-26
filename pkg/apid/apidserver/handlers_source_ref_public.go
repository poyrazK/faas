package apidserver

// Public-repository source refs (ADR-20261126164847). A source-ref deploy or
// scan first goes through an installation of the account, as before. Only
// when none sees the repository does apid ask githubd to read it as a public
// repository: no installation, no branch intent, no binding. githubd proves
// the repository is public on every read; apid limits how often an account
// may ask, because every account shares githubd's one public-read budget.

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// githubdPublicSourceClient is optional so customer-path fakes and the stub
// client need not implement it; liveClient does. Without it apid keeps the
// installation-only behavior.
type githubdPublicSourceClient interface {
	StreamPublicSourceRef(ctx context.Context, repoFullName, ref string, maxArchiveBytes int64) (*StreamSourceRefResult, error)
}

// publicSourceFetchKind keys the per-account public-read counter.
const publicSourceFetchKind = "public_source_fetch"

var publicSourceFetchSlot = state.MailSlotLimit{Window: time.Hour, MaxPerWindow: api.PublicSourceFetchesPerHour}

// sourceRefAccess is how one source-ref read reaches GitHub.
type sourceRefAccess struct {
	installID int64
	public    bool
	// installProblem is the installation path's refusal. A public read that
	// finds no public repository answers it unchanged, so a private
	// repository gets exactly the answer it got before public reads existed.
	installProblem *api.Problem
}

// publicSourceFallback reports whether the installation path's refusal means
// "no installation of this account sees the repository", the only refusal a
// public read may answer instead. Transient failures never fall back.
func publicSourceFallback(p *api.Problem) bool {
	return p != nil && (p.Code == api.CodeGitHubRepoNotAccessible || p.Code == api.CodeGitHubInstallNotFound)
}

// sourceRefAccessFor turns the installation lookup's result into the access a
// read uses. allowPublic is false when the request needs an installation (a
// reviewed or branch-pinned source, or an explicitly chosen installation).
// The public_source_deploys platform flag releases the fallback; off, the
// account gets the installation path's answer.
func (s *server) sourceRefAccessFor(acct state.Account, installID int64, installProblem *api.Problem, allowPublic bool) (sourceRefAccess, *api.Problem) {
	if installProblem == nil {
		return sourceRefAccess{installID: installID}, nil
	}
	if !allowPublic || !publicSourceFallback(installProblem) {
		return sourceRefAccess{}, installProblem
	}
	if !s.platformFlagFor(platformflags.PublicSourceDeploys, platformflags.Subject{AccountID: acct.ID, Plan: string(acct.Plan)}) {
		return sourceRefAccess{}, installProblem
	}
	if _, ok := s.githubd.(githubdPublicSourceClient); !ok {
		return sourceRefAccess{}, installProblem
	}
	if p := s.takePublicSourceFetch(acct); p != nil {
		return sourceRefAccess{}, p
	}
	return sourceRefAccess{public: true, installProblem: installProblem}, nil
}

// resolveSourceRefDeployAccess is the deploy's installation lookup with the
// public fallback. Reviewed and branch-pinned deploys keep requiring an
// installation: their evidence names one. So does an app bound to this
// repository: its binding's installation failing means push-to-deploy is
// broken, which a public deploy would hide.
func (s *server) resolveSourceRefDeployAccess(ctx context.Context, acct state.Account, app state.App, req api.SourceRefDeployRequest) (sourceRefAccess, *api.Problem) {
	installID, problem := s.resolveInstallToken(ctx, acct, app, req.Repo)
	allowPublic := req.ExpectedSource == nil && req.SourceBranch == ""
	if problem != nil && allowPublic && s.appBoundToRepo(ctx, acct, app, req.Repo) {
		allowPublic = false
	}
	return s.sourceRefAccessFor(acct, installID, problem, allowPublic)
}

// appBoundToRepo reports whether app has a GitHub binding for repo. A lookup
// failure counts as bound, so an unknown binding never falls back.
func (s *server) appBoundToRepo(ctx context.Context, acct state.Account, app state.App, repo string) bool {
	binding, err := s.store.GetGithubInstallBindingForApp(ctx, app.ID, acct.ID)
	if errors.Is(err, state.ErrNotFound) {
		return false
	}
	return err != nil || canonicalGitHubRepo(binding.RepoFullName) == canonicalGitHubRepo(repo)
}

// sourceRefDeployBranch records branch intent only for installation reads; a
// public deploy is pinned to its commit (the deployments pair CHECK needs an
// installation for a branch).
func (s *server) sourceRefDeployBranch(ctx context.Context, accountID string, access sourceRefAccess, repo, ref string) (string, *api.Problem) {
	if access.public {
		return "", nil
	}
	return s.resolveSourceRefBranch(ctx, accountID, access.installID, repo, ref)
}

// takePublicSourceFetch counts one public read against
// PublicSourceFetchesPerHour.
func (s *server) takePublicSourceFetch(acct state.Account) *api.Problem {
	s.publicSourceFetchesOnce.Do(func() { s.publicSourceFetches = newSecurityMailLimiter() })
	if acct.ID == "" {
		return api.ErrInternal("could not count this account's public repository reads")
	}
	if s.publicSourceFetches.allowWithin(acct.ID, publicSourceFetchKind, publicSourceFetchSlot) {
		return nil
	}
	retry := s.publicSourceFetches.retryAfter(acct.ID, publicSourceFetchKind, publicSourceFetchSlot.Window)
	return api.ErrPublicSourceFetchRateLimited(api.PublicSourceFetchesPerHour, int((retry+time.Second-1)/time.Second))
}

// streamSourceRefArchive opens the archive through the access's path.
func (s *server) streamSourceRefArchive(ctx context.Context, installOwner string, access sourceRefAccess, repo, ref string, maxArchiveBytes int64) (*StreamSourceRefResult, *api.Problem) {
	if !access.public {
		return s.streamSourceTarball(ctx, installOwner, access.installID, repo, ref, maxArchiveBytes)
	}
	client, ok := s.githubd.(githubdPublicSourceClient)
	if !ok {
		return nil, access.installProblem
	}
	res, err := client.StreamPublicSourceRef(ctx, repo, ref, maxArchiveBytes)
	if err != nil {
		if p := api.AsProblem(access.publicReadError(err)); p != nil {
			return nil, p
		}
		return nil, api.ErrSourceRefUnavailable("stream public source tarball failed")
	}
	if res == nil || res.Body == nil {
		return nil, api.ErrSourceRefUnavailable("githubd returned empty stream")
	}
	stats := &StreamSourceRefStats{}
	return &StreamSourceRefResult{Body: &publicSourceBody{ReadCloser: res.Body, remote: res.Stats, local: stats, access: access}, Stats: stats}, nil
}

// publicReadError answers "not a public repository", and a githubd without
// the public RPC, with the installation path's refusal.
func (a sourceRefAccess) publicReadError(err error) error {
	if err == nil {
		return nil
	}
	if status.Code(err) == codes.Unimplemented {
		return a.installProblem
	}
	if p := api.AsProblem(err); p != nil && p.Code == api.CodeGitHubRepoNotAccessible {
		return a.installProblem
	}
	return err
}

// publicSourceBody applies publicReadError to the stream's read and terminal
// errors, which is where a streaming RPC reports most failures.
type publicSourceBody struct {
	io.ReadCloser
	remote *StreamSourceRefStats
	local  *StreamSourceRefStats
	access sourceRefAccess
	once   sync.Once
	err    error
}

func (b *publicSourceBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		err = b.access.publicReadError(err)
	}
	return n, err
}

func (b *publicSourceBody) Close() error {
	b.once.Do(func() {
		b.err = b.access.publicReadError(b.ReadCloser.Close())
		if b.remote == nil {
			return
		}
		*b.local = *b.remote
		b.local.Err = b.access.publicReadError(b.remote.Err)
	})
	return b.err
}

// retryAfter is how long until the oldest counted event of accountID and kind
// leaves window, so a refused caller knows when to retry.
func (l *securityMailLimiter) retryAfter(accountID, kind string, window time.Duration) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	sent := l.sent[accountID+"\x00"+kind]
	if len(sent) == 0 {
		return 0
	}
	return max(sent[0].Add(window).Sub(l.now()), 0)
}
