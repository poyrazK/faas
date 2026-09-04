// Source-tree interface for githubd's push-dispatch path (PR-H,
// repo decomposition Phase 5), plus the headless source-ref stream
// for DEPLOY-PROV-4 / ADR-092 (issue #739).
//
// githubd.Service.HandlePushRequest needs to read the on-disk tree
// for the pushed commit so pkg/reconcile.Scan can run on it. The
// transport (HTTP codeload archive + tar.gz extraction) lives in
// pkg/gitfetch (PR-GH.3) — credential-free, fs.FS-shaped. The
// adapter that closes the loop (resolve install row → unseal
// token → call gitfetch.Fetcher → hand back a Tree) lives in
// cmd/githubd/source_fetcher.go and implements SourceFetcher.
//
// The headless source-ref deploy path (POST /v1/apps/{slug}/
// deployments/source-ref) needs the same install token + repo
// resolution but, instead of an extracted fs.FS, wants the raw
// tarball bytes written into a staging file. SourceRefStreamer is
// that seam: it owns the install-row lookup, the install-token
// unseal (via TokenCache), and the codeload GET; it returns an
// io.ReadCloser of the response body wrapped in
// io.LimitReader(maxArchiveBytes + 1, …). The caller (apid's
// handleSourceRefDeploy) is responsible for piping the body into
// validateAndSpool.
//
// Splitting the interface from the implementation keeps pkg/githubd
// free of pgx/age/secretbox imports (the package has unit tests
// that exercise HandlePushRequest end-to-end without a real DB or
// real age keypair). The interface is also the seam future slices
// need: PR-H.2 replaces codeload with a self-hosted mirror by
// swapping the concrete implementation behind this interface.

package githubd

import (
	"context"
	"io"
	"io/fs"
)

// SourceTree is the read-only filesystem view of one commit's tree
// after the fetcher has downloaded and extracted the archive. The
// interface intentionally mirrors gitfetch.Tree so the cmd/githubd
// adapter can hand the package-local value straight through. We
// re-declare it here so pkg/githubd stays decoupled from
// pkg/gitfetch at the type-system level — a future migration off
// pkg/gitfetch doesn't require editing pkg/githubd.
type SourceTree interface {
	FS() fs.FS
	Close() error
}

// SourceFetcher downloads + extracts the source tree for one
// (accountID, installID, repoFullName, commitSHA) tuple.
// Implementations must:
//
//   - Use accountID to look up the durable install row (the
//     account → install mapping is one-to-one per §11 oauth-handshake
//     model). The install row carries the sealed install token that
//     authenticates the archive download.
//   - Verify installID matches the install row's InstallationID.
//     Mismatch is a hard error (ErrNoBinding) — a malicious push
//     that lies about its install_id must never reach the archive
//     endpoint under the wrong install's token (PR-A's audit
//     pipeline rejects takeover attempts, but the daemon-side guard
//     is load-bearing).
//   - Scope the bearer token to a single Fetch call. The token must
//     NEVER be stored on the receiver; the cmd/githubd adapter
//     unseals it on demand and the production Fetcher uses it for
//     exactly one Authorization header per Fetch.
//   - Honour ctx cancellation. A reconcile that bails out must not
//     leave the fetcher spinning until its own timeout.
//   - Return a Tree whose Close() is idempotent. githubd's webhook
//     handler defers tree.Close() so a panic in reconcile doesn't
//     leak the temp dir.
type SourceFetcher interface {
	Fetch(ctx context.Context, accountID string, installID int64, repoFullName, commitSHA string) (SourceTree, error)
}

// SourceRefStreamer streams the raw tar.gz bytes for a (repo, ref)
// pair over the durable install's GitHub App installation token
// (DEPLOY-PROV-4 / ADR-092, issue #739). It is the gRPC bridge
// behind POST /v1/apps/{slug}/deployments/source-ref — apid dials
// it via StreamSourceRef, pipes the returned io.ReadCloser into
// validateAndSpool (cmd/apid/deploy_inputs.go:218), then os.Rename's
// the staged file to FAAS_SPOOL_ROOT/<id>.tar.gz.
//
// Implementations must:
//
//   - Resolve the install row for accountID (the install token is
//     sealed at rest in state.GitHubInstall.SealedToken) and verify
//     installID matches inst.InstallationID. A mismatch is returned
//     as ErrNoBinding so a stale or forged bridge request cannot
//     use a different installation's token.
//   - Mint (or refresh) the install token via TokenCache.Token —
//     singleflight + 5-min proactive refresh keeps the cached
//     token under 55 min of GitHub's 1 h TTL. On 401 from the
//     commit or codeload request: TokenCache.Invalidate + retry once.
//     On retry
//     401: surface ErrSourceRefUnavailable (mirrors
//     source_fetcher.go's ErrUnauthorized / ErrNotFound
//     surface).
//   - Enforce maxArchiveBytes on the streaming body via
//     io.LimitReader — capBytes=0 disables the cap (tests).
//   - Honour ctx cancellation: a cancelled apid handler bails
//     before the tarball is fully read; the body is drained +
//     closed in the same defer.
//   - Scope the bearer token to a single Stream call. The token
//     must NEVER escape this function — not in logs, not in
//     URLs, not in the response body, not in any error string.
type SourceRefStreamer interface {
	Stream(ctx context.Context, accountID string, installID int64, repoFullName, ref string, maxArchiveBytes int64) (SourceRefStream, error)
}

// SourceRefStream carries the raw archive body and the immutable commit SHA
// resolved for the requested branch, tag, or short SHA.
type SourceRefStream struct {
	Body              io.ReadCloser
	ResolvedCommitSHA string
}
