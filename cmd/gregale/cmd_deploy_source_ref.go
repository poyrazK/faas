// cmd_deploy_source_ref.go — headless source-ref deploy (issue #739,
// DEPLOY-PROV-4 / ADR-092). CLI half of the PR-A server foundation
// (cmd/apid/handlers_source_ref.go + the apid↔githubd gRPC bridge).
//
// Replaces the M7.5 dashboard browser flow (the previous cmdDeployRepo
// at commands2.go:1040, deleted in PR-B). The CLI never touches the
// GitHub install token — apid resolves it server-side from
// github_installations via MintInstallationToken, fetches the
// codeload tarball through StreamSourceRef, spools it under
// FAAS_SPOOL_ROOT, validates shape, and enqueues a
// DeploymentKindGitHub build row. The audit row `deploy.source_ref`
// fires server-side (auditSourceRefDeploy); this CLI never emits
// audit data.
//
// CI acceptance gate (closes issue #739):
//
//	FAAS_API=https://api.gregale.dev \
//	FAAS_TOKEN=$FAAS_TOKEN \
//	gregale deploy --repo OWNER/NAME --ref $(git rev-parse HEAD)
//
// with no GREGALE_INSTALL_TOKEN_* env vars set.
//
// Annotations (issue #977 / ADR-116) ride on this path as
// `--reason` / `--tag` / `--deployed-by` flags (cmdDeployTarball)
// and are forwarded into the JSON wire. The GitHub Action path
// runs gregale deploy with explicit values for all three; local CLI
// invocations resolveDeployedBy() picks the actor label.

package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// cmdDeployRepoSourceRef posts {repo, ref, format:"tarball"} to the
// PR-A endpoint and streams the build log. The deploy command supplies
// a stable logical retry key; the SDK scopes it to the source-ref
// transport before sending it to apid. Calls through the legacy wrapper
// retain the SDK's UUID fallback.
//
// On 409 source_ref_unavailable the server sets Retry-After:30; we
// surface the value via printErr + Problem.HasHeader so the operator
// sees a precise backoff hint instead of a bare 503.
//
// jsonOutput true → single JSON-encoded DeploymentResponse on
// osStdout (matches the existing Deploy/DeployTarball wire shape). The
// caller's lifecycle mode decides whether it is queued or terminal.
// jsonOutput false → streamDeployLogs tail the SSE build log.
// cmdDeployRepoSourceRef posts {repo, ref, format:"tarball", annotations...}
// to the PR-A endpoint and streams the build log. The annotation
// fields (issue #977 / ADR-116) ride on the JSON wire directly
// (SourceRefDeployRequest is the JSON body — distinct from the
// multipart DeployTarball path). The GitHub Action defaults
// DeployedBy to ${{ github.actor }} and PRNumber to
// ${{ github.event.pull_request.number }} when present, so the CI
// path stays zero-friction. Local CLI invocations call this with
// whatever resolveDeployedBy produced (which may be the auto-
// captured git config user.name).
func cmdDeployRepoSourceRef(slug, repo, ref string, ann api.DeployAnnotations) int {
	return cmdDeployRepoSourceRefContext(context.Background(), slug, repo, ref, ann)
}

func cmdDeployRepoSourceRefContext(ctx context.Context, slug, repo, ref string, ann api.DeployAnnotations) int {
	return cmdDeployRepoSourceRefContextWithWait(ctx, slug, repo, ref, ann, true)
}

// cmdDeployRepoSourceRefContextWithWait is retained for direct legacy callers.
// The top-level deploy command uses the options helper below and supplies its
// output-independent lifecycle decision explicitly.
func cmdDeployRepoSourceRefContextWithWait(ctx context.Context, slug, repo, ref string, ann api.DeployAnnotations, waitForDeploy bool) int {
	return cmdDeployRepoSourceRefContextWithJSONWait(ctx, slug, repo, ref, ann, waitForDeploy, false)
}

// cmdDeployRepoSourceRefContextWithJSONWait is the source-ref equivalent of
// the local/image deploy paths. jsonWait selects a terminal machine-readable
// receipt; false selects either a queued receipt or the human progress stream.
func cmdDeployRepoSourceRefContextWithJSONWait(ctx context.Context, slug, repo, ref string, ann api.DeployAnnotations, waitForDeploy, jsonWait bool) int {
	return cmdDeployRepoSourceRefContextWithJSONWaitOptions(ctx, slug, repo, ref, ann, waitForDeploy, jsonWait, "", defaultDeployWaitTimeout)
}

// cmdDeployRepoSourceRefContextWithJSONWaitOptions is the timeout- and
// retry-aware source-ref entry point used by `gregale deploy`. The legacy
// wrapper above keeps the helper's default behavior for callers that do not
// need the deploy-local flags.
func cmdDeployRepoSourceRefContextWithJSONWaitOptions(ctx context.Context, slug, repo, ref string, ann api.DeployAnnotations, waitForDeploy, jsonWait bool, idempotencyKey string, waitTimeout time.Duration) int {
	return cmdDeployRepoSourceRefContextWithJSONWaitOptionsAndManifest(ctx, slug, repo, ref, ann, waitForDeploy, jsonWait, idempotencyKey, waitTimeout, false)
}

func cmdDeployRepoSourceRefContextWithJSONWaitOptionsAndManifest(ctx context.Context, slug, repo, ref string, ann api.DeployAnnotations, waitForDeploy, jsonWait bool, idempotencyKey string, waitTimeout time.Duration, noTriggers bool) int {
	return cmdDeployRepoSourceRefContextWithJSONWaitOptionsAndManifestAndRollout(ctx, slug, repo, ref, ann, waitForDeploy, jsonWait, idempotencyKey, waitTimeout, noTriggers, false)
}

func cmdDeployRepoSourceRefContextWithJSONWaitOptionsAndManifestAndRollout(ctx context.Context, slug, repo, ref string, ann api.DeployAnnotations, waitForDeploy, jsonWait bool, idempotencyKey string, waitTimeout time.Duration, noTriggers, waitForRollout bool) int {
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	// Source-ref is a first-deploy transport as well as a redeploy transport.
	// Probe first so an existing app can redeploy even when the account is at
	// its app cap; create only after the account-scoped lookup returns 404.
	app, err := ensureSourceRefApp(ctx, client, slug)
	if err != nil {
		return printErr("Could not create or fetch app", err)
	}
	appURL := canonicalAppURL(app)
	req := api.SourceRefDeployRequest{
		Repo:           repo,
		Ref:            ref,
		Format:         "tarball",
		Environment:    ann.Environment,
		Reason:         ann.Reason,
		Tag:            ann.Tag,
		DeployedBy:     ann.DeployedBy,
		PRNumber:       ann.PRNumber,
		TrafficPercent: ann.TrafficPercent,
		Canary:         ann.Canary,
		NoTriggers:     noTriggers,
		RollbackOn5xx:  ann.RollbackOn5xx,
	}
	deployCtx := ctx
	if idempotencyKey != "" {
		deployCtx = api.ContextWithIdempotencyKey(ctx, deployOperationIdempotencyKey(idempotencyKey, "source-ref"))
	}
	dep, err := client.DeployFromSourceRef(deployCtx, slug, req)
	if err != nil {
		// errors.As (not type-assert) so a future wrapping in
		// the SDK chain (e.g. fmt.Errorf("%w: …")) still surfaces
		// the APIError. The SDK currently returns *APIError
		// directly, but the assertion-via-As is the lint-clean
		// shape (errorlint) and future-proof.
		var apiErr *api.APIError
		if errors.As(err, &apiErr) {
			// Surface the Retry-After hint on transient githubd
			// / codeload blips so the operator doesn't have to
			// reach for the audit log to figure out the backoff.
			if ra := apiErr.Problem.HasHeader("Retry-After"); len(ra) > 0 {
				return printErr("Source-ref unavailable",
					fmt.Errorf("%s (Retry-After: %ss)", apiErr.Problem.Code, ra[0]))
			}
		}
		return printErr("Deploy failed", err)
	}
	if jsonOutput && !jsonWait {
		// Issue #1182 §P1 follow-up: source-ref path has no
		// client-side tarball bytes (server pulls the codeload
		// tarball via the GitHub App install token) and no git
		// detection (prov == nil), so the receipt's only delta
		// over the bare DeploymentResponse is app_url. Prefer the
		// server-resolved canonical URL, falling back to the CLI-known
		// slug (not the 32-hex AppID) so the URL remains routable.
		// Commit pinning for the source-ref path is captured
		// server-side; see docs/source-ref.md reproducibility
		// section.
		return jsonOut(writeJSON(newDeployReceipt(dep, nil, appURL, "")))
	}
	if !waitForDeploy {
		PrintOK(osStdout, "Deployment %s queued. %s", dep.ID, appURL)
		return 0
	}
	if jsonWait {
		return writeWaitedDeploymentReceiptUntilWithOptions(ctx, client, dep, nil, appURL, "", slug, waitTimeout, waitForRollout)
	}
	return streamDeployLogsContextWithOptions(ctx, client, dep, slug, streamDeployOptions{waitTimeout: waitTimeout, waitForRollout: waitForRollout})
}

func ensureSourceRefApp(ctx context.Context, client *Client, slug string) (api.AppResponse, error) {
	if app, err := client.GetApp(ctx, slug); err == nil {
		return app, nil
	} else if !isNotFound(err) {
		return api.AppResponse{}, err
	}
	if app, err := client.CreateApp(ctx, buildCreateRequest(slug, shapeApp, "", nil, nil)); err == nil {
		return app, nil
	} else {
		var ae *APIError
		if !errors.As(err, &ae) || ae.Problem.Status != http.StatusConflict {
			return api.AppResponse{}, err
		}
	}
	// A peer may have reserved the same account-owned slug between the GET
	// miss and CreateApp. Retry once; an IDOR-shaped 404 means another account
	// owns it and the caller should choose a different name.
	app, err := client.GetApp(ctx, slug)
	if err != nil {
		return api.AppResponse{}, fmt.Errorf("slug %q is already in use; pick a different --name", slug)
	}
	return app, nil
}
