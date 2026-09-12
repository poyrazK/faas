/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { GitHubInstallMutationRequest } from '../models/GitHubInstallMutationRequest.js';
import type { GitHubInstallStatus } from '../models/GitHubInstallStatus.js';
import type { InstallBindRequest } from '../models/InstallBindRequest.js';
import type { InstallBindResponse } from '../models/InstallBindResponse.js';
import type { RepoResponse } from '../models/RepoResponse.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class GithubService {
  /**
   * List repos the user's GitHub App installation can see.
   * Cookie-session-authenticated (NOT API-key). Hydrates the
   * dashboard bind picker's repo dropdown. Returns the repos
   * visible to the user's GitHub App installation — githubd
   * resolves the per-install token from the session's account.
   *
   * §11 anti-takeover: the handler re-runs
   * githubd.VerifyInstallation with the session's github_login
   * as `expected_login` before listing. Mismatch → 403 forged.
   *
   * @returns RepoResponse Repos the install can see.
   * @throws ApiError
   */
  public static listInstallableRepos({
    requestBody,
  }: {
    requestBody: InstallBindRequest,
  }): CancelablePromise<Array<RepoResponse>> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/install/repos/list',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: invalid_request — missing/malformed body or installation_id.`,
        403: `\`forged\` if the install's account.login differs from
        the session's github_login; \`github_login_required\` if
        the user hasn't completed /v1/auth/github. Returned for
        POST /v1/install/repos/list — the bind picker refuses
        to list repos of an install the user can't prove they own.
        `,
        502: `code: github_unreachable — githubd could not reach api.github.com; retry in a minute.`,
        503: `code: githubd_not_ready — githubd is not wired on this host (M7.5 slices 7-8).`,
      },
    });
  }
  /**
   * List repositories visible to the account's GitHub App installation.
   * Bearer API-key surface for customer automation. Requires the
   * dedicated `github:manage` scope. The account's durable GitHub App
   * installation is resolved server-side, so callers do not need to
   * copy or provide an installation id. Installation credentials are
   * never returned.
   *
   * @returns RepoResponse Repositories currently visible to the account's GitHub App installation.
   * @throws ApiError
   */
  public static listGitHubRepositories(): CancelablePromise<Array<RepoResponse>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/github/repos',
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `The account has not completed GitHub App installation.`,
        502: `GitHub could not be reached while listing repositories.`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Read the GitHub installation and repository binding for an app.
   * Bearer API-key surface for customer automation. Requires the
   * dedicated `github:manage` scope and never returns a CSRF token or
   * installation credentials.
   *
   * @returns GitHubInstallStatus Automation-safe installation and binding snapshot.
   * @throws ApiError
   */
  public static getGitHubConnection({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<GitHubInstallStatus> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/github',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Remove an app's GitHub repository binding.
   * Bearer API-key surface for customer automation. Requires
   * `github:manage`; the account-level GitHub installation remains
   * available for a later bind. Safe to retry with an idempotency key.
   *
   * @returns GitHubInstallStatus Automation disconnect completed; the installation remains available.
   * @throws ApiError
   */
  public static disconnectGitHubConnection({
    slug,
    idempotencyKey,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<GitHubInstallStatus> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/github',
      path: {
        'slug': slug,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Bind or rebind an app to a visible GitHub repository.
   * Bearer API-key surface for customer automation. Requires
   * `github:manage`. The installation_id must belong to the account and
   * the requested repository must currently be visible to that GitHub App
   * installation. Safe to retry with an idempotency key.
   *
   * @returns InstallBindResponse Automation bind completed and the repository edge is active.
   * @throws ApiError
   */
  public static bindGitHubConnection({
    slug,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: InstallBindRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<InstallBindResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/github/bind',
      path: {
        'slug': slug,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `Installation ownership or repository-access proof failed.`,
        404: `code: not_found`,
        409: `code: conflict`,
        502: `GitHub could not be reached.`,
      },
    });
  }
  /**
   * Check an app's GitHub repository access now.
   * Bearer API-key surface for customer automation. Requires
   * `github:manage`; GitHub is queried and the app is detached only when
   * its bound repository is no longer accessible.
   *
   * @returns GitHubInstallStatus Post-reconciliation connection projection.
   * @throws ApiError
   */
  public static syncGitHubConnection({
    slug,
    idempotencyKey,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<GitHubInstallStatus> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/github/sync',
      path: {
        'slug': slug,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        502: `The GitHub repository catalog was unavailable during reconciliation.`,
      },
    });
  }
  /**
   * Inspect the app's GitHub repository binding.
   * Cookie-session-authenticated (NOT API-key). Returns the durable
   * GitHub installation metadata and the app's repository binding without
   * exposing installation credentials. The response also carries the
   * named CSRF token required by the sync and disconnect actions.
   *
   * @returns GitHubInstallStatus Current installation and binding state.
   * @throws ApiError
   */
  public static getGitHubInstallBinding({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<GitHubInstallStatus> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/install/bind',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        503: `code: capacity — the installation and binding state could not be loaded.`,
      },
    });
  }
  /**
   * Persist the (account, app, installation, repo, branch) bind row.
   * Cookie-session-authenticated (NOT API-key). Persists the
   * GitHub install binding via githubd.BindAppRepo (which
   * writes through to pkg/state.PgStore.UpsertGithubInstallBinding
   * per PR-B).
   *
   * §11 anti-takeover: the handler re-runs
   * githubd.VerifyInstallation with the session's github_login
   * as `expected_login` before persisting. Mismatch → 403 forged.
   * Empty github_login → 403 github_login_required.
   *
   * On success emits `auth.install.bound` for the audit trail.
   *
   * @returns InstallBindResponse Bind persisted.
   * @throws ApiError
   */
  public static bindAppInstall({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: InstallBindRequest,
  }): CancelablePromise<InstallBindResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/install/bind',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: invalid_request — missing/malformed body, installation_id, or repo_full_name.`,
        403: `\`forged\` if the install's account.login differs from
        the session's github_login; \`github_login_required\` if
        the user hasn't completed /v1/auth/github. Returned for
        POST /v1/apps/{slug}/install/bind — the bind refuses to
        persist a row for an install the user can't prove they own.
        `,
        404: `code: not_found`,
        502: `code: github_unreachable — githubd could not reach api.github.com when persisting the bind; retry in a minute.`,
      },
    });
  }
  /**
   * Disconnect an app from its GitHub repository.
   * Cookie-session-authenticated and CSRF-protected. Idempotently removes
   * the app's repository binding and invalidates githubd's binding cache.
   * The GitHub App installation remains connected to the account so it can
   * be rebound to another repository.
   *
   * @returns GitHubInstallStatus The app is no longer bound to a repository.
   * @throws ApiError
   */
  public static disconnectGitHubApp({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: GitHubInstallMutationRequest,
  }): CancelablePromise<GitHubInstallStatus> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/install/bind',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation — missing or invalid CSRF token for disconnect.`,
        404: `code: not_found`,
        503: `code: capacity — githubd could not remove the binding.`,
      },
    });
  }
  /**
   * Read the GitHub installation and binding health for an app.
   * Cookie-session-authenticated (NOT API-key). This is the canonical
   * dashboard status endpoint. It returns `not_installed`, `installed`,
   * or `bound` state and never includes sealed installation credentials.
   * A named CSRF token is returned for subsequent sync or disconnect
   * requests.
   *
   * @returns GitHubInstallStatus Current GitHub connection state.
   * @throws ApiError
   */
  public static getGitHubInstallStatus({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<GitHubInstallStatus> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/install',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        503: `code: capacity — the connection state could not be read.`,
      },
    });
  }
  /**
   * Reconcile an app's GitHub repository access immediately.
   * Cookie-session-authenticated and CSRF-protected. Queries GitHub's
   * current installation repository list and detaches the app only when
   * its bound repository is no longer accessible. The response includes
   * the remote repository count and whether this app was detached.
   *
   * @returns GitHubInstallStatus Reconciled connection state.
   * @throws ApiError
   */
  public static syncGitHubInstall({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: GitHubInstallMutationRequest,
  }): CancelablePromise<GitHubInstallStatus> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/install/sync',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation — a valid CSRF token is required for GitHub sync.`,
        409: `code: github_not_bound or github_install_not_found — the app or installation must be connected first.`,
        502: `code: github_unreachable — GitHub could not be queried.`,
        503: `code: capacity — the connection state could not be reconciled.`,
      },
    });
  }
}
