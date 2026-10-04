/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APIKeyResponse } from '../models/APIKeyResponse.js';
import type { ApplicationStandardEnrollment } from '../models/ApplicationStandardEnrollment.js';
import type { ApplicationStandardException } from '../models/ApplicationStandardException.js';
import type { ApplicationStandardExceptionList } from '../models/ApplicationStandardExceptionList.js';
import type { ApplicationStandardList } from '../models/ApplicationStandardList.js';
import type { ApplicationStandardLogDestination } from '../models/ApplicationStandardLogDestination.js';
import type { ApplicationStandardLogDestinationList } from '../models/ApplicationStandardLogDestinationList.js';
import type { ApplicationStandardOperation } from '../models/ApplicationStandardOperation.js';
import type { ApplicationStandardPublisher } from '../models/ApplicationStandardPublisher.js';
import type { ApplicationStandardPublisherList } from '../models/ApplicationStandardPublisherList.js';
import type { ApplicationStandardReview } from '../models/ApplicationStandardReview.js';
import type { ApplicationStandardReviewRequest } from '../models/ApplicationStandardReviewRequest.js';
import type { ApplicationStandardVersion } from '../models/ApplicationStandardVersion.js';
import type { AppResponse } from '../models/AppResponse.js';
import type { ApproveApplicationStandardExceptionRequest } from '../models/ApproveApplicationStandardExceptionRequest.js';
import type { ApproveApplicationStandardReviewRequest } from '../models/ApproveApplicationStandardReviewRequest.js';
import type { ChangeMemberRoleRequest } from '../models/ChangeMemberRoleRequest.js';
import type { ControlApplicationStandardOperationRequest } from '../models/ControlApplicationStandardOperationRequest.js';
import type { CreateApplicationStandardLogDestinationRequest } from '../models/CreateApplicationStandardLogDestinationRequest.js';
import type { CreateApplicationStandardPublisherRequest } from '../models/CreateApplicationStandardPublisherRequest.js';
import type { CreateApplicationStandardVersionRequest } from '../models/CreateApplicationStandardVersionRequest.js';
import type { CreateAppRequest } from '../models/CreateAppRequest.js';
import type { CreateOrgAPIKeyRequest } from '../models/CreateOrgAPIKeyRequest.js';
import type { CreateOrgRequest } from '../models/CreateOrgRequest.js';
import type { InvitationListResponse } from '../models/InvitationListResponse.js';
import type { InvitationWithTokenResponse } from '../models/InvitationWithTokenResponse.js';
import type { InviteMemberRequest } from '../models/InviteMemberRequest.js';
import type { ListOrgActivityResponse } from '../models/ListOrgActivityResponse.js';
import type { ListOrgAPIKeysResponse } from '../models/ListOrgAPIKeysResponse.js';
import type { MemberListResponse } from '../models/MemberListResponse.js';
import type { OrgAppListResponse } from '../models/OrgAppListResponse.js';
import type { OrgInvitationResponse } from '../models/OrgInvitationResponse.js';
import type { OrgListResponse } from '../models/OrgListResponse.js';
import type { OrgMemberResponse } from '../models/OrgMemberResponse.js';
import type { OrgResponse } from '../models/OrgResponse.js';
import type { PatchOrgRequest } from '../models/PatchOrgRequest.js';
import type { RevokeApplicationStandardExceptionRequest } from '../models/RevokeApplicationStandardExceptionRequest.js';
import type { RotateOrgAPIKeyRequest } from '../models/RotateOrgAPIKeyRequest.js';
import type { RotateOrgAPIKeyResponse } from '../models/RotateOrgAPIKeyResponse.js';
import type { SeatUsageResponse } from '../models/SeatUsageResponse.js';
import type { SetApplicationStandardLocalIntentRequest } from '../models/SetApplicationStandardLocalIntentRequest.js';
import type { TransferOwnershipRequest } from '../models/TransferOwnershipRequest.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class OrgsService {
  /**
   * List API keys minted against the active org.
   * Returns every key the org owns (active + grace + revoked).
   * Mirrors `GET /v1/keys`; PR 6's canonical path. The `org_id`
   * on every row will match `{slug}` because the store filters
   * server-side on the loaded membership.
   *
   * @returns ListOrgAPIKeysResponse Org-scoped API key list.
   * @throws ApiError
   */
  public static listOrgApiKeys({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<ListOrgAPIKeysResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{slug}/keys',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `Org slug not found, or caller has no membership.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Mint a new API key for the active org.
   * Returns the plaintext exactly once (same as `POST /v1/keys`).
   * The new row's `org_id` is the loaded membership's org; personal
   * orgs are mintable (the `org_personal_immutable` 409 applies to
   * mutations on the org row, not key mints against it).
   *
   * @returns APIKeyResponse New API key minted against the org.
   * @throws ApiError
   */
  public static createOrgApiKey({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: CreateOrgAPIKeyRequest,
  }): CancelablePromise<APIKeyResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{slug}/keys',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid body (unknown scope, label too long).`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `Active-org header missing or unknown; caller has no membership on the resolved slug.`,
        429: `Per-account key quota (\`api.Plan.KeysMax\`) reached.`,
      },
    });
  }
  /**
   * Fetch a single API key by id (org-scoped).
   * Lookup mirror of `GET /v1/keys/{id}` (the legacy path does not
   * exist by id in pre-PR-6 — this path is the canonical single-key
   * read). The response is the standard `APIKeyResponse` (no
   * plaintext). Cross-org probes collapse to 404.
   *
   * @returns APIKeyResponse Single API key.
   * @throws ApiError
   */
  public static getOrgApiKey({
    slug,
    id,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<APIKeyResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{slug}/keys/{id}',
      path: {
        'slug': slug,
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `Key id not in org, or org slug not found.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Revoke an API key (org-scoped).
   * Soft-delete mirror of `DELETE /v1/keys/{id}`. Status flips to
   * 'revoked'; subsequent bearer-auth attempts hit `ErrAPIKeyRevoked`
   * (401 unauthenticated). The audit row carries `org_id` (PR 6
   * closes the ADR-061 §E "audit scoped to org" gap).
   *
   * @returns void
   * @throws ApiError
   */
  public static revokeOrgApiKey({
    slug,
    id,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/orgs/{slug}/keys/{id}',
      path: {
        'slug': slug,
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `Key id not in this org, or active-org slug not found.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Rotate an API key (org-scoped).
   * Org-scoped counterpart of `POST /v1/keys/{id}/rotate`. Mints a
   * new key (status='active') and demotes the predecessor into the
   * grace window in one transaction. The new key inherits the
   * predecessor's `org_id` — rotation never silently rebinds across
   * orgs. Quota is neutral (-1 +1 = 0).
   *
   * @returns RotateOrgAPIKeyResponse New key minted, predecessor in 'grace' (or 'revoked' if grace_window_days=0).
   * @throws ApiError
   */
  public static rotateOrgApiKey({
    slug,
    id,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    requestBody?: RotateOrgAPIKeyRequest,
  }): CancelablePromise<RotateOrgAPIKeyResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{slug}/keys/{id}/rotate',
      path: {
        'slug': slug,
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `Key id not in org, or org slug not found, or predecessor already revoked.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * List orgs the caller has an active membership in.
   * Returns the personal org + every shared org the caller
   * belongs to. Account-scoped (no `X-Active-Org` header needed).
   * The list is sorted by slug.
   *
   * @returns OrgListResponse The org list (may contain at most 1 personal + N shared).
   * @throws ApiError
   */
  public static listOrgs(): CancelablePromise<OrgListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/orgs',
      errors: {
        401: `code: unauthorized`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Create a shared org (caller becomes the first owner).
   * Mints a new shared (non-personal) org + the caller's owner
   * membership in one transaction. The slug must match
   * `^[a-z0-9][a-z0-9-]{1,30}[a-z0-9]$` (3..32 chars); name is
   * trimmed-non-empty (1..256 chars). Personal orgs cannot be
   * created via this endpoint — every account already has a
   * personal org (PR 3 backfill / migration 00099).
   *
   * @returns OrgResponse The new shared org.
   * @throws ApiError
   */
  public static createOrg({
    requestBody,
    idempotencyKey,
  }: {
    requestBody: CreateOrgRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<OrgResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/orgs',
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        409: `\`409 Conflict\` — slug already taken (\`org_slug_taken\`) or the
        caller is already a member of an org with the same slug
        (\`org_already_member\`).
        `,
        422: `\`422 Unprocessable Entity\` — slug violates the \`OrgSlugPattern\`
        regex (lowercase alphanumeric + dashes, 3..32 chars).
        Stable code \`org_slug_invalid\`.
        `,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Get one org by slug.
   * Returns the org row + the caller's role on the org. Authz:
   * any active member of the org (`org.view`); non-members see
   * 403 `org_role_forbidden`. Unknown slugs are 404
   * `org_not_found` (IDOR-safe — same wire shape as the
   * pkg/authz.LoadOrg middleware).
   *
   * @returns OrgResponse The org + the caller's role.
   * @throws ApiError
   */
  public static getOrg({
    slug,
  }: {
    /**
     * Org slug. Lowercase letters, digits, hyphens; must start
     * and end with alnum. 3..32 chars. Mirrors `OrgSlugPattern`
     * in `pkg/api/errors.go` exactly so the spec drift gate
     * (`make spec-check`) stays green.
     *
     */
    slug: string,
  }): CancelablePromise<OrgResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{slug}',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        403: `\`403 Forbidden\` — caller is authenticated but lacks the
        \`org.view\` action on the target org. Stable code
        \`org_role_forbidden\`.
        `,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Update the org (name and/or plan).
   * Partial update; both fields are pointer-typed so the
   * handler distinguishes "omitted" from "clear". Authz
   * routing:
   * - `name` → `org.manage_billing` (owner + billing)
   * - `plan` → `org.change_plan` (owner only)
   * Personal orgs are immutable (`org_personal_immutable` 409).
   *
   * @returns OrgResponse The updated org.
   * @throws ApiError
   */
  public static updateOrg({
    slug,
    requestBody,
  }: {
    /**
     * Org slug. Lowercase letters, digits, hyphens; must start
     * and end with alnum. 3..32 chars. Mirrors `OrgSlugPattern`
     * in `pkg/api/errors.go` exactly so the spec drift gate
     * (`make spec-check`) stays green.
     *
     */
    slug: string,
    requestBody: PatchOrgRequest,
  }): CancelablePromise<OrgResponse> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/orgs/{slug}',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `\`403 Forbidden\` — caller lacks \`org.manage_billing\` (name)
        or \`org.change_plan\` (plan), OR the resulting member count
        would exceed the plan cap (\`OrgMembersMax\`).
        Stable codes \`org_role_forbidden\` | \`org_member_cap_exceeded\`.
        `,
        404: `code: not_found`,
        409: `\`409 Conflict\` — the target org is the caller's personal
        org (created by the PR 3 backfill); personal orgs cannot be soft-deleted.
        immutable and cannot be modified by PATCH. Stable code
        \`org_personal_immutable\`.
        `,
        422: `\`422 Unprocessable Entity\` — slug violates the
        \`OrgSlugPattern\` regex (lowercase alphanumeric + dashes,
        3..32 chars). Stable code \`org_slug_invalid\`.
        `,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Soft-delete the org.
   * Sets `status='deleted_pending'` + `deleted_pending=true`
   * on the row (PR 5). Hard delete + GDPR purge lands in PR 8.
   * Authz: owner-only (`org.delete`). Personal orgs are
   * immutable.
   *
   * @returns void
   * @throws ApiError
   */
  public static deleteOrg({
    slug,
  }: {
    /**
     * Org slug. Lowercase letters, digits, hyphens; must start
     * and end with alnum. 3..32 chars. Mirrors `OrgSlugPattern`
     * in `pkg/api/errors.go` exactly so the spec drift gate
     * (`make spec-check`) stays green.
     *
     */
    slug: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/orgs/{slug}',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        403: `\`403 Forbidden\` — caller is authenticated but lacks the
        \`org.delete\` action. Owner-only. Stable code
        \`org_role_forbidden\`.
        `,
        404: `code: not_found`,
        409: `\`409 Conflict\` — the target org is the caller's personal
        org (created by the PR 3 backfill); personal orgs are immutable on PATCH (name/plan).
        immutable and cannot be modified by PATCH. Stable code
        \`org_personal_immutable\`.
        `,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * List the organization's global infrastructure activity.
   * Returns one newest-first timeline across applications, deployments,
   * environment configuration, domains, certificates, and automated
   * platform actions. Every active member may read it (`org.view`).
   *
   * Entries are a curated customer-facing projection, not raw provider or
   * security audit payloads. Labels are captured at write time and `data`
   * contains non-secret display metadata only; environment values and
   * credentials are never included. Pass `next_before` back unchanged as
   * `before` to fetch the next older page.
   *
   * @returns ListOrgActivityResponse A stable keyset page of organization activity.
   * @throws ApiError
   */
  public static listOrgActivity({
    slug,
    before,
    limit = 50,
    kindPrefix,
    actorType,
    appId,
  }: {
    /**
     * Org slug. Lowercase letters, digits, hyphens; must start
     * and end with alnum. 3..32 chars. Mirrors `OrgSlugPattern`
     * in `pkg/api/errors.go` exactly so the spec drift gate
     * (`make spec-check`) stays green.
     *
     */
    slug: string,
    /**
     * Opaque cursor returned as `next_before` by the prior page.
     */
    before?: string,
    /**
     * Maximum number of activity items to return in this page.
     */
    limit?: number,
    /**
     * Optional namespaced kind prefix, such as `deploy.`.
     */
    kindPrefix?: string,
    /**
     * Restrict results to one captured actor category.
     */
    actorType?: 'user' | 'api_key' | 'github' | 'system' | 'operator',
    /**
     * Restrict results to activity associated with this application.
     */
    appId?: string,
  }): CancelablePromise<ListOrgActivityResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{slug}/activity',
      path: {
        'slug': slug,
      },
      query: {
        'before': before,
        'limit': limit,
        'kind_prefix': kindPrefix,
        'actor_type': actorType,
        'app_id': appId,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `Workspace app inventory requires the \`org.view\` action.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Preview an assignment change without activating it
   * @returns ApplicationStandardReview Saved review preview; assignments and applications are unchanged.
   * @throws ApiError
   */
  public static previewApplicationStandardAssignment({
    slug,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * Org slug. Lowercase letters, digits, hyphens; must start
     * and end with alnum. 3..32 chars. Mirrors `OrgSlugPattern`
     * in `pkg/api/errors.go` exactly so the spec drift gate
     * (`make spec-check`) stays green.
     *
     */
    slug: string,
    requestBody: ApplicationStandardReviewRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<ApplicationStandardReview> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{slug}/application-standard-reviews',
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
        403: `Caller lacks the required scope, organization action or completed MFA.`,
        404: `code: not_found`,
        409: `Assignment changed or inputs are busy; refresh and retry the preview.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `Application standards storage is temporarily unavailable.`,
      },
    });
  }
  /**
   * Approve an exact saved review and queue a controlled rollout
   * Release gated and disabled by default. Requires a current organization owner or administrator, write scope and completed MFA. Approval rechecks the exact saved plan; operator controls compare the exact expected_updated_at from a current read. Installed settings and consumer observation remain separate. Aborting does not undo installed targets; rollback requires a fresh assignment preview and approval under current constraints.
   * @returns ApplicationStandardOperation Durable operation intent and retained target progress; consumer convergence is pending.
   * @throws ApiError
   */
  public static approveApplicationStandardReview({
    slug,
    review,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * Org slug. Lowercase letters, digits, hyphens; must start
     * and end with alnum. 3..32 chars. Mirrors `OrgSlugPattern`
     * in `pkg/api/errors.go` exactly so the spec drift gate
     * (`make spec-check`) stays green.
     *
     */
    slug: string,
    review: string,
    requestBody: ApproveApplicationStandardReviewRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<ApplicationStandardOperation> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{slug}/application-standard-reviews/{review}/approve',
      path: {
        'slug': slug,
        'review': review,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `Caller lacks the required scope, current organization action or completed MFA.`,
        404: `code: not_found`,
        409: `Review changed or expired, inputs are busy, or the operation timestamp or state changed.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `Application standards mutations are disabled or storage is unavailable.`,
      },
    });
  }
  /**
   * Pause outstanding targets in a controlled rollout
   * Release gated and disabled by default. Requires a current organization owner or administrator, write scope and completed MFA. Approval rechecks the exact saved plan; operator controls compare the exact expected_updated_at from a current read. Installed settings and consumer observation remain separate. Aborting does not undo installed targets; rollback requires a fresh assignment preview and approval under current constraints.
   * @returns ApplicationStandardOperation Durable operation intent and retained target progress; consumer convergence is pending.
   * @throws ApiError
   */
  public static pauseApplicationStandardOperation({
    slug,
    operation,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * Org slug. Lowercase letters, digits, hyphens; must start
     * and end with alnum. 3..32 chars. Mirrors `OrgSlugPattern`
     * in `pkg/api/errors.go` exactly so the spec drift gate
     * (`make spec-check`) stays green.
     *
     */
    slug: string,
    operation: string,
    requestBody: ControlApplicationStandardOperationRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<ApplicationStandardOperation> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{slug}/application-standard-operations/{operation}/pause',
      path: {
        'slug': slug,
        'operation': operation,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `Caller lacks the required scope, current organization action or completed MFA.`,
        404: `code: not_found`,
        409: `Review changed or expired, inputs are busy, or the operation timestamp or state changed.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `Application standards mutations are disabled or storage is unavailable.`,
      },
    });
  }
  /**
   * Resume a paused controlled rollout
   * Release gated and disabled by default. Requires a current organization owner or administrator, write scope and completed MFA. Approval rechecks the exact saved plan; operator controls compare the exact expected_updated_at from a current read. Installed settings and consumer observation remain separate. Aborting does not undo installed targets; rollback requires a fresh assignment preview and approval under current constraints.
   * @returns ApplicationStandardOperation Durable operation intent and retained target progress; consumer convergence is pending.
   * @throws ApiError
   */
  public static resumeApplicationStandardOperation({
    slug,
    operation,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * Org slug. Lowercase letters, digits, hyphens; must start
     * and end with alnum. 3..32 chars. Mirrors `OrgSlugPattern`
     * in `pkg/api/errors.go` exactly so the spec drift gate
     * (`make spec-check`) stays green.
     *
     */
    slug: string,
    operation: string,
    requestBody: ControlApplicationStandardOperationRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<ApplicationStandardOperation> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{slug}/application-standard-operations/{operation}/resume',
      path: {
        'slug': slug,
        'operation': operation,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `Caller lacks the required scope, current organization action or completed MFA.`,
        404: `code: not_found`,
        409: `Review changed or expired, inputs are busy, or the operation timestamp or state changed.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `Application standards mutations are disabled or storage is unavailable.`,
      },
    });
  }
  /**
   * Abort outstanding targets while retaining installed facts
   * Release gated and disabled by default. Requires a current organization owner or administrator, write scope and completed MFA. Approval rechecks the exact saved plan; operator controls compare the exact expected_updated_at from a current read. Installed settings and consumer observation remain separate. Aborting does not undo installed targets; rollback requires a fresh assignment preview and approval under current constraints.
   * @returns ApplicationStandardOperation Durable operation intent and retained target progress; consumer convergence is pending.
   * @throws ApiError
   */
  public static abortApplicationStandardOperation({
    slug,
    operation,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * Org slug. Lowercase letters, digits, hyphens; must start
     * and end with alnum. 3..32 chars. Mirrors `OrgSlugPattern`
     * in `pkg/api/errors.go` exactly so the spec drift gate
     * (`make spec-check`) stays green.
     *
     */
    slug: string,
    operation: string,
    requestBody: ControlApplicationStandardOperationRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<ApplicationStandardOperation> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{slug}/application-standard-operations/{operation}/abort',
      path: {
        'slug': slug,
        'operation': operation,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `Caller lacks the required scope, current organization action or completed MFA.`,
        404: `code: not_found`,
        409: `Review changed or expired, inputs are busy, or the operation timestamp or state changed.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `Application standards mutations are disabled or storage is unavailable.`,
      },
    });
  }
  /**
   * Inspect a saved review, including expiry and blockers
   * @returns ApplicationStandardReview Organization-scoped saved standards data.
   * @throws ApiError
   */
  public static getApplicationStandardReview({
    slug,
    review,
  }: {
    /**
     * Org slug. Lowercase letters, digits, hyphens; must start
     * and end with alnum. 3..32 chars. Mirrors `OrgSlugPattern`
     * in `pkg/api/errors.go` exactly so the spec drift gate
     * (`make spec-check`) stays green.
     *
     */
    slug: string,
    review: string,
  }): CancelablePromise<ApplicationStandardReview> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{slug}/application-standard-reviews/{review}',
      path: {
        'slug': slug,
        'review': review,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `Caller lacks the required scope, organization action or completed MFA.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `Application standards storage is temporarily unavailable.`,
      },
    });
  }
  /**
   * Inspect saved rollout targets and progress
   * @returns ApplicationStandardOperation Organization-scoped saved standards data.
   * @throws ApiError
   */
  public static getApplicationStandardOperation({
    slug,
    operation,
  }: {
    /**
     * Org slug. Lowercase letters, digits, hyphens; must start
     * and end with alnum. 3..32 chars. Mirrors `OrgSlugPattern`
     * in `pkg/api/errors.go` exactly so the spec drift gate
     * (`make spec-check`) stays green.
     *
     */
    slug: string,
    operation: string,
  }): CancelablePromise<ApplicationStandardOperation> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{slug}/application-standard-operations/{operation}',
      path: {
        'slug': slug,
        'operation': operation,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `Caller lacks the required scope, organization action or completed MFA.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `Application standards storage is temporarily unavailable.`,
      },
    });
  }
  /**
   * List approval, revocation and expiry history for a live application
   * UUID-ordered history with status evaluated at as_of. A full page has a next cursor; the final follow-up page may be empty. Historical approval does not imply that the exception contributes to current effective settings.
   * @returns ApplicationStandardExceptionList Organization-scoped saved standards data.
   * @throws ApiError
   */
  public static listApplicationStandardExceptions({
    slug,
    app,
    after,
    limit = 100,
  }: {
    /**
     * Org slug. Lowercase letters, digits, hyphens; must start
     * and end with alnum. 3..32 chars. Mirrors `OrgSlugPattern`
     * in `pkg/api/errors.go` exactly so the spec drift gate
     * (`make spec-check`) stays green.
     *
     */
    slug: string,
    app: string,
    after?: string,
    limit?: number,
  }): CancelablePromise<ApplicationStandardExceptionList> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{slug}/application-standard-enrollments/{app}/exceptions',
      path: {
        'slug': slug,
        'app': app,
      },
      query: {
        'after': after,
        'limit': limit,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `Caller lacks the required scope, organization action or completed MFA.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `Application standards storage is temporarily unavailable.`,
      },
    });
  }
  /**
   * Approve a bounded application exception
   * Requires an active owner or admin. Approves one field for an adopted immutable standard version; all independent constraints and platform controls still apply. The server enforces a future expiry within 30 days and queues a new desired revision.
   * Disabled by default until the application standards release acceptance gates pass.
   * Idempotency replay requires the current role and release gate. Saved intent is not consumer observation.
   *
   * @returns ApplicationStandardException Saved intent or retained exception approval; worker installation and consumer convergence remain separate.
   * @throws ApiError
   */
  public static approveApplicationStandardException({
    slug,
    app,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * Org slug. Lowercase letters, digits, hyphens; must start
     * and end with alnum. 3..32 chars. Mirrors `OrgSlugPattern`
     * in `pkg/api/errors.go` exactly so the spec drift gate
     * (`make spec-check`) stays green.
     *
     */
    slug: string,
    app: string,
    requestBody: ApproveApplicationStandardExceptionRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<ApplicationStandardException> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{slug}/application-standard-enrollments/{app}/exceptions',
      path: {
        'slug': slug,
        'app': app,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `Caller lacks the write scope, required organization action or completed MFA.`,
        404: `code: not_found`,
        409: `Enrollment revision changed, an exception already exists, or installation or a controlled operation is pending.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `Standards mutations are disabled by the release gate, or storage is unavailable. Disabled requests return application_standards_pending and make no change.`,
      },
    });
  }
  /**
   * Read application standards intent, installed settings and observation progress.
   * Requires org.view_application_standards and a read-scoped credential, with MFA for sessions.
   * Local choices and desired revision describe saved intent. installed_effective describes
   * the last installed projection and is absent before installation. Neither persisted
   * settings nor this read establish consumer observation. Reads remain available while mutations are disabled.
   *
   * @returns ApplicationStandardEnrollment Scoped enrollment with saved adoption pins and installed field provenance.
   * @throws ApiError
   */
  public static getApplicationStandardEnrollment({
    slug,
    app,
  }: {
    /**
     * Org slug. Lowercase letters, digits, hyphens; must start
     * and end with alnum. 3..32 chars. Mirrors `OrgSlugPattern`
     * in `pkg/api/errors.go` exactly so the spec drift gate
     * (`make spec-check`) stays green.
     *
     */
    slug: string,
    /**
     * UUID of a live application belonging to this organization.
     */
    app: string,
  }): CancelablePromise<ApplicationStandardEnrollment> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{slug}/application-standard-enrollments/{app}',
      path: {
        'slug': slug,
        'app': app,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `Caller lacks the read scope or organization action, or must complete MFA.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `Application standards storage is temporarily unavailable.`,
      },
    });
  }
  /**
   * Replace permitted local application settings
   * Requires an active owner, admin or developer. settings and additional_log_destinations replace the complete local intent; explicit empty values clear it. Mandatory settings and permitted extensions are checked atomically against the current desired revision. The response retains the last installed projection while a changed desired revision waits for installation.
   * Disabled by default until the application standards release acceptance gates pass.
   * Idempotency replay requires the current role and release gate. Saved intent is not consumer observation.
   *
   * @returns ApplicationStandardEnrollment Saved intent or retained exception approval; worker installation and consumer convergence remain separate.
   * @throws ApiError
   */
  public static setApplicationStandardLocalIntent({
    slug,
    app,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * Org slug. Lowercase letters, digits, hyphens; must start
     * and end with alnum. 3..32 chars. Mirrors `OrgSlugPattern`
     * in `pkg/api/errors.go` exactly so the spec drift gate
     * (`make spec-check`) stays green.
     *
     */
    slug: string,
    app: string,
    requestBody: SetApplicationStandardLocalIntentRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<ApplicationStandardEnrollment> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/orgs/{slug}/application-standard-enrollments/{app}/local-intent',
      path: {
        'slug': slug,
        'app': app,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `Caller lacks the write scope, required organization action or completed MFA.`,
        404: `code: not_found`,
        409: `Enrollment revision changed, an exception already exists, or installation or a controlled operation is pending.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `Standards mutations are disabled by the release gate, or storage is unavailable. Disabled requests return application_standards_pending and make no change.`,
      },
    });
  }
  /**
   * Revoke an application exception and retain its approval history
   * Requires an active owner or admin and the current desired revision. The original approval, reason and value remain immutable; revocation queues reevaluation under the ordinary inherited controls. A conflicting local setting can leave the application blocked until it is corrected.
   * Disabled by default until the application standards release acceptance gates pass.
   * Idempotency replay requires the current role and release gate. Saved intent is not consumer observation.
   *
   * @returns ApplicationStandardException Saved intent or retained exception approval; worker installation and consumer convergence remain separate.
   * @throws ApiError
   */
  public static revokeApplicationStandardException({
    slug,
    app,
    exception,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * Org slug. Lowercase letters, digits, hyphens; must start
     * and end with alnum. 3..32 chars. Mirrors `OrgSlugPattern`
     * in `pkg/api/errors.go` exactly so the spec drift gate
     * (`make spec-check`) stays green.
     *
     */
    slug: string,
    app: string,
    exception: string,
    requestBody: RevokeApplicationStandardExceptionRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<ApplicationStandardException> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{slug}/application-standard-enrollments/{app}/exceptions/{exception}/revoke',
      path: {
        'slug': slug,
        'app': app,
        'exception': exception,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `Caller lacks the write scope, required organization action or completed MFA.`,
        404: `code: not_found`,
        409: `Enrollment revision changed, an exception already exists, or installation or a controlled operation is pending.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `Standards mutations are disabled by the release gate, or storage is unavailable. Disabled requests return application_standards_pending and make no change.`,
      },
    });
  }
  /**
   * List the latest published application standard versions.
   * Requires org.view_application_standards. Publication does not activate a standard.
   * @returns ApplicationStandardList Latest versions, ordered by standard slug.
   * @throws ApiError
   */
  public static listApplicationStandards({
    slug,
    after,
    limit = 100,
  }: {
    /**
     * Org slug. Lowercase letters, digits, hyphens; must start
     * and end with alnum. 3..32 chars. Mirrors `OrgSlugPattern`
     * in `pkg/api/errors.go` exactly so the spec drift gate
     * (`make spec-check`) stays green.
     *
     */
    slug: string,
    /**
     * Exclusive standard slug from next_page_after.
     */
    after?: string,
    /**
     * Maximum standard versions returned in this page.
     */
    limit?: number,
  }): CancelablePromise<ApplicationStandardList> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{slug}/application-standards',
      path: {
        'slug': slug,
      },
      query: {
        'after': after,
        'limit': limit,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `Caller cannot list standards in this organization.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Read an immutable application standard version.
   * Requires org.view_application_standards. Omitting version returns the latest published candidate.
   * @returns ApplicationStandardVersion Immutable version with canonical definition hash and publishing identity.
   * @throws ApiError
   */
  public static getApplicationStandardVersion({
    slug,
    standard,
    version,
  }: {
    /**
     * Org slug. Lowercase letters, digits, hyphens; must start
     * and end with alnum. 3..32 chars. Mirrors `OrgSlugPattern`
     * in `pkg/api/errors.go` exactly so the spec drift gate
     * (`make spec-check`) stays green.
     *
     */
    slug: string,
    /**
     * Organization-local standard slug.
     */
    standard: string,
    /**
     * Immutable candidate version; omit to read the latest.
     */
    version?: number,
  }): CancelablePromise<ApplicationStandardVersion> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{slug}/application-standards/{standard}',
      path: {
        'slug': slug,
        'standard': standard,
      },
      query: {
        'version': version,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `Caller cannot inspect the requested standard version.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Publish a candidate application standard version.
   * Requires org.manage_application_standards (owner or admin), MFA and deploy-write scope.
   * expected_version is 0 for a new standard and the latest version for an update.
   * Publication creates immutable history and does not change an assignment or application.
   * A concurrent publication returns application_standard_version_stale without writes.
   *
   * @returns ApplicationStandardVersion Published candidate version.
   * @throws ApiError
   */
  public static publishApplicationStandardVersion({
    slug,
    standard,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * Org slug. Lowercase letters, digits, hyphens; must start
     * and end with alnum. 3..32 chars. Mirrors `OrgSlugPattern`
     * in `pkg/api/errors.go` exactly so the spec drift gate
     * (`make spec-check`) stays green.
     *
     */
    slug: string,
    /**
     * Slug receiving the new immutable version.
     */
    standard: string,
    requestBody: CreateApplicationStandardVersionRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<ApplicationStandardVersion> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{slug}/application-standards/{standard}/versions',
      path: {
        'slug': slug,
        'standard': standard,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `Caller cannot manage application standards in this organization.`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * List immutable organization-owned standard log-destinations.
   * Requires org.view_application_standards to list logging destination references.
   * @returns ApplicationStandardLogDestinationList Page of logging destination references ordered by ID.
   * @throws ApiError
   */
  public static listApplicationStandardLogDestinations({
    slug,
    after,
    limit = 100,
  }: {
    /**
     * Org slug. Lowercase letters, digits, hyphens; must start
     * and end with alnum. 3..32 chars. Mirrors `OrgSlugPattern`
     * in `pkg/api/errors.go` exactly so the spec drift gate
     * (`make spec-check`) stays green.
     *
     */
    slug: string,
    /**
     * Exclusive logging destination ID from next_page_after.
     */
    after?: string,
    /**
     * Maximum logging destination references in this page.
     */
    limit?: number,
  }): CancelablePromise<ApplicationStandardLogDestinationList> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{slug}/application-standard-log-destinations',
      path: {
        'slug': slug,
      },
      query: {
        'after': after,
        'limit': limit,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `Caller cannot list logging destination references.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Create an immutable organization-owned logging destination.
   * Requires org.manage_application_standards to create logging destination references (owner or admin).
   * Existing resources cannot be edited. Rotation creates a new reference and
   * requires a new standard version to adopt it. Resource creation does not
   * enroll or change any service. Customer responses and audit events omit sealed credentials.
   *
   * @returns ApplicationStandardLogDestination Created immutable logging destination reference.
   * @throws ApiError
   */
  public static createApplicationStandardLogDestination({
    slug,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * Org slug. Lowercase letters, digits, hyphens; must start
     * and end with alnum. 3..32 chars. Mirrors `OrgSlugPattern`
     * in `pkg/api/errors.go` exactly so the spec drift gate
     * (`make spec-check`) stays green.
     *
     */
    slug: string,
    requestBody: CreateApplicationStandardLogDestinationRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<ApplicationStandardLogDestination> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{slug}/application-standard-log-destinations',
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
        403: `Caller cannot create logging destination references in this organization.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Read an immutable logging destination reference.
   * Requires org.view_application_standards to read this logging destination in its owning organization.
   * @returns ApplicationStandardLogDestination Requested logging destination metadata.
   * @throws ApiError
   */
  public static getApplicationStandardLogDestination({
    slug,
    resource,
  }: {
    /**
     * Org slug. Lowercase letters, digits, hyphens; must start
     * and end with alnum. 3..32 chars. Mirrors `OrgSlugPattern`
     * in `pkg/api/errors.go` exactly so the spec drift gate
     * (`make spec-check`) stays green.
     *
     */
    slug: string,
    /**
     * Immutable logging destination UUID.
     */
    resource: string,
  }): CancelablePromise<ApplicationStandardLogDestination> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{slug}/application-standard-log-destinations/{resource}',
      path: {
        'slug': slug,
        'resource': resource,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `Caller cannot read this logging destination.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * List immutable organization-owned standard publishers.
   * Requires org.view_application_standards to list publisher key references.
   * @returns ApplicationStandardPublisherList Page of publisher key references ordered by ID.
   * @throws ApiError
   */
  public static listApplicationStandardPublishers({
    slug,
    after,
    limit = 100,
  }: {
    /**
     * Org slug. Lowercase letters, digits, hyphens; must start
     * and end with alnum. 3..32 chars. Mirrors `OrgSlugPattern`
     * in `pkg/api/errors.go` exactly so the spec drift gate
     * (`make spec-check`) stays green.
     *
     */
    slug: string,
    /**
     * Exclusive publisher key ID from next_page_after.
     */
    after?: string,
    /**
     * Maximum publisher key references in this page.
     */
    limit?: number,
  }): CancelablePromise<ApplicationStandardPublisherList> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{slug}/application-standard-publishers',
      path: {
        'slug': slug,
      },
      query: {
        'after': after,
        'limit': limit,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `Caller cannot list publisher key references.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Create an immutable organization-owned publisher key.
   * Requires org.manage_application_standards to create publisher key references (owner or admin).
   * Existing resources cannot be edited. Rotation creates a new reference and
   * requires a new standard version to adopt it. Resource creation does not
   * enroll or change any service. Customer responses and audit events omit sealed credentials.
   *
   * @returns ApplicationStandardPublisher Created immutable publisher key reference.
   * @throws ApiError
   */
  public static createApplicationStandardPublisher({
    slug,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * Org slug. Lowercase letters, digits, hyphens; must start
     * and end with alnum. 3..32 chars. Mirrors `OrgSlugPattern`
     * in `pkg/api/errors.go` exactly so the spec drift gate
     * (`make spec-check`) stays green.
     *
     */
    slug: string,
    requestBody: CreateApplicationStandardPublisherRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<ApplicationStandardPublisher> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{slug}/application-standard-publishers',
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
        403: `Caller cannot create publisher key references in this organization.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Read an immutable publisher key reference.
   * Requires org.view_application_standards to read this publisher key in its owning organization.
   * @returns ApplicationStandardPublisher Requested publisher key metadata.
   * @throws ApiError
   */
  public static getApplicationStandardPublisher({
    slug,
    resource,
  }: {
    /**
     * Org slug. Lowercase letters, digits, hyphens; must start
     * and end with alnum. 3..32 chars. Mirrors `OrgSlugPattern`
     * in `pkg/api/errors.go` exactly so the spec drift gate
     * (`make spec-check`) stays green.
     *
     */
    slug: string,
    /**
     * Immutable publisher key UUID.
     */
    resource: string,
  }): CancelablePromise<ApplicationStandardPublisher> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{slug}/application-standard-publishers/{resource}',
      path: {
        'slug': slug,
        'resource': resource,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `Caller cannot read this publisher key.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * List apps attributed to this workspace.
   * Returns a newest-first inventory of non-deleted apps whose persisted
   * `org_id` matches this workspace. Any active member with `org.view`
   * may read the minimal summary; creator identity and app configuration
   * are intentionally omitted while app-specific routes remain
   * creator-scoped.
   *
   * @returns OrgAppListResponse Safe summary of apps attributed to this workspace.
   * @throws ApiError
   */
  public static listOrgApps({
    slug,
  }: {
    /**
     * Org slug. Lowercase letters, digits, hyphens; must start
     * and end with alnum. 3..32 chars. Mirrors `OrgSlugPattern`
     * in `pkg/api/errors.go` exactly so the spec drift gate
     * (`make spec-check`) stays green.
     *
     */
    slug: string,
  }): CancelablePromise<OrgAppListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{slug}/apps',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        403: `Caller is not an active member with \`org.view\`.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Create an app attributed to the organization.
   * Creates an app in this workspace and records the workspace id on the
   * app row, so app lifecycle activity is attributed to this organization.
   * The creator remains the account-level quota and billing identity in
   * this rollout slice; existing app-specific routes remain creator-scoped
   * until shared app access is migrated in follow-up work. Owners, admins,
   * and developers may create apps (`org.create_app`).
   *
   * @returns AppResponse The newly created organization-attributed app.
   * @throws ApiError
   */
  public static createOrgApp({
    slug,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * Org slug. Lowercase letters, digits, hyphens; must start
     * and end with alnum. 3..32 chars. Mirrors `OrgSlugPattern`
     * in `pkg/api/errors.go` exactly so the spec drift gate
     * (`make spec-check`) stays green.
     *
     */
    slug: string,
    /**
     * App creation payload. See CreateAppRequest.
     */
    requestBody: CreateAppRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<AppResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{slug}/apps',
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
        403: `Caller is not verified or lacks \`org.create_app\`.`,
        404: `code: not_found`,
        409: `code: conflict`,
        422: `code: invalid_cpu_ram_pair — explicit ram_mb and vcpu do not match the canonical shape for the account plan.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * List active members of the org.
   * Returns the active membership rows (the store returns
   * both active + removed; the handler filters at the API
   * boundary). Each row carries the joined `account.email`
   * so the dashboard can render `bob@acme.com` without a
   * second round-trip. Removed rows do NOT count toward the
   * member cap (per ADR-061 §B).
   *
   * @returns MemberListResponse The active member list.
   * @throws ApiError
   */
  public static listOrgMembers({
    slug,
  }: {
    /**
     * Org slug. Lowercase letters, digits, hyphens; must start
     * and end with alnum. 3..32 chars. Mirrors `OrgSlugPattern`
     * in `pkg/api/errors.go` exactly so the spec drift gate
     * (`make spec-check`) stays green.
     *
     */
    slug: string,
  }): CancelablePromise<MemberListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{slug}/members',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        403: `\`403 Forbidden\` — caller is authenticated but lacks the
        \`org.view\` action. Stable code \`org_role_forbidden\`.
        `,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Invite a new member (returns plaintext token ONCE).
   * Mints a 32-byte plaintext token, hashes it via SHA-256 for
   * storage, and returns the plaintext ONCE in the response.
   * The token expires after 14 days; admins can revoke earlier by
   * row ID via `DELETE /v1/orgs/{slug}/invitations/{invitation_id}` (PR 7 owns
   * the accept surface too — see
   * `POST /v1/invitations/{token}/accept`). Role cannot be
   * `owner`; transfer-ownership is the only path to owner.
   *
   * @returns InvitationWithTokenResponse The invitation + the one-time plaintext token.
   * @throws ApiError
   */
  public static addOrgMember({
    slug,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * Org slug. Lowercase letters, digits, hyphens; must start
     * and end with alnum. 3..32 chars. Mirrors `OrgSlugPattern`
     * in `pkg/api/errors.go` exactly so the spec drift gate
     * (`make spec-check`) stays green.
     *
     */
    slug: string,
    requestBody: InviteMemberRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<InvitationWithTokenResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{slug}/members',
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
        403: `\`403 Forbidden\` — caller lacks \`org.invite_members\`, OR
        the resulting pending-invitation count would exceed the
        plan cap (\`OrgPendingInvitationsMax\`).
        Stable codes \`org_role_forbidden\` | \`org_invitation_cap_exceeded\`.
        `,
        409: `\`409 Conflict\` — the target org is the caller's personal
        org (created by the PR 3 backfill); personal orgs are immutable on POST /members (invite).
        immutable and cannot be modified by PATCH. Stable code
        \`org_personal_immutable\`.
        `,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Change a member's role.
   * Owner-only (`org.change_role`). Role cannot be `owner` on
   * this endpoint; transfer-ownership is the only path to owner.
   * The exactly-one-owner invariant lives in
   * `pkg/state::UpdateOrgMemberRole`'s tx; demoting the last
   * active owner surfaces as 409 `org_last_owner`.
   *
   * @returns OrgMemberResponse The updated member row.
   * @throws ApiError
   */
  public static updateOrgMemberRole({
    slug,
    userId,
    requestBody,
  }: {
    /**
     * Org slug. Lowercase letters, digits, hyphens; must start
     * and end with alnum. 3..32 chars. Mirrors `OrgSlugPattern`
     * in `pkg/api/errors.go` exactly so the spec drift gate
     * (`make spec-check`) stays green.
     *
     */
    slug: string,
    /**
     * Account UUID (the membership row's account_id). The
     * path segment is named `user_id` for backwards
     * compatibility with the dashboard's existing
     * /members/:user_id routes.
     *
     */
    userId: string,
    requestBody: ChangeMemberRoleRequest,
  }): CancelablePromise<OrgMemberResponse> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/orgs/{slug}/members/{user_id}',
      path: {
        'slug': slug,
        'user_id': userId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `\`403 Forbidden\` — caller is authenticated but lacks the
        \`org.change_role\` action. Owner-only. Stable code
        \`org_role_forbidden\`.
        `,
        404: `\`404 Not Found\` — caller is authenticated but the
        \`{user_id}\` path param does not correspond to any active
        member of the target org, OR the target org belongs to
        another tenant (IDOR-safe cross-tenant collapse).
        Stable code \`org_role_forbidden\`.
        `,
        409: `\`409 Conflict\` — caller attempted to demote or remove
        the only remaining active owner of the org. The
        exactly-one-owner invariant lives on the
        \`org_memberships_one_owner_idx\` partial unique index
        (migration 00099). Stable code \`org_last_owner\`.
        `,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Remove a member.
   * Owner-only (`org.remove_members`). Stamps `removed_at` on
   * the row (the row stays for audit; live-cap count drops).
   * Self-removal is rejected at the boundary; the last-owner
   * invariant surfaces as 409 `org_last_owner`.
   *
   * @returns void
   * @throws ApiError
   */
  public static removeOrgMember({
    slug,
    userId,
  }: {
    /**
     * Org slug. Lowercase letters, digits, hyphens; must start
     * and end with alnum. 3..32 chars. Mirrors `OrgSlugPattern`
     * in `pkg/api/errors.go` exactly so the spec drift gate
     * (`make spec-check`) stays green.
     *
     */
    slug: string,
    /**
     * Account UUID (the membership row's account_id). The
     * path segment is named `user_id` for backwards
     * compatibility with the dashboard's existing
     * /members/:user_id routes.
     *
     */
    userId: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/orgs/{slug}/members/{user_id}',
      path: {
        'slug': slug,
        'user_id': userId,
      },
      errors: {
        401: `code: unauthorized`,
        403: `\`403 Forbidden\` — caller is authenticated but lacks the
        \`org.remove_members\` action. Owner-only. Self-removal
        is refused at the boundary. Stable code
        \`org_role_forbidden\`.
        `,
        404: `\`404 Not Found\` — caller is authenticated but the
        target slug is unknown, OR \`{user_id}\` does not
        correspond to an active member, OR the org belongs
        to another tenant (IDOR-safe cross-tenant collapse).
        Stable codes \`org_role_forbidden\` | \`NotFound\`.
        `,
        409: `\`409 Conflict\` — caller attempted to remove (DELETE) the only
        the only remaining active owner of the org. The
        exactly-one-owner invariant lives on the
        \`org_memberships_one_owner_idx\` partial unique index
        (migration 00099). Stable code \`org_last_owner\`.
        `,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Transfer ownership to another active member.
   * Atomically promotes `new_owner_account_id` to owner and
   * demotes the caller to admin via `Store.TransferOrgOwnership`
   * (a single PostgreSQL tx with `FOR UPDATE` locks on both
   * rows). The exactly-one-owner invariant is enforced by the
   * partial unique `org_memberships_one_owner_idx`
   * (migration 00099). The new owner must already be an active
   * member of the org.
   *
   * @returns OrgResponse The org (post-transfer).
   * @throws ApiError
   */
  public static transferOrgOwnership({
    slug,
    requestBody,
  }: {
    /**
     * Org slug. Lowercase letters, digits, hyphens; must start
     * and end with alnum. 3..32 chars. Mirrors `OrgSlugPattern`
     * in `pkg/api/errors.go` exactly so the spec drift gate
     * (`make spec-check`) stays green.
     *
     */
    slug: string,
    requestBody: TransferOwnershipRequest,
  }): CancelablePromise<OrgResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{slug}/transfer_ownership',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `\`403 Forbidden\` — caller is authenticated but is not the
        active owner of the target org. Stable code
        \`org_role_forbidden\`.
        `,
        404: `code: NotFound (new owner is not a member)`,
        409: `code: org_last_owner (caller not the active owner OR new owner already owner)`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Peek at a pending invitation by token (no consumption).
   * Read-only lookup that returns the invitation metadata
   * (email, role, org slug, expires_at) without consuming the
   * token. Used by the dashboard to render "you've been invited
   * to Acme Inc. as developer" without forcing the invitee
   * to accept yet. PR 7 added the accept surface at
   * `POST /v1/invitations/{token}/accept`.
   *
   * @returns OrgInvitationResponse The pending invitation.
   * @throws ApiError
   */
  public static peekInvitation({
    token,
  }: {
    /**
     * Plaintext or base64url-encoded invitation token. Tokens are
     * returned ONCE on the create call (`POST /v1/orgs/{slug}/members`)
     * and never re-served.
     *
     */
    token: string,
  }): CancelablePromise<OrgInvitationResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/invitations/{token}',
      path: {
        'token': token,
      },
      errors: {
        401: `code: unauthorized`,
        410: `code: org_invitation_invalid | org_invitation_expired | org_invitation_invalid (already consumed/revoked)`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Accept an invitation token (consume + add as member).
   * Consumes the invitation via `Store.ConsumeOrgInvitation` —
   * the load-bearing tx stamps `consumed_at`, inserts the active
   * membership, and reads the live member cap (PR 2 cap-in-tx
   * back-stop; documents at `pkg/state/memstore.go`). Two audit
   * rows fire post-mutation per ADR-035: `org.invitation.accepted`
   * (invitation-side record) and `org.member.added` (member-side
   * record). The bearer must have a valid session or API key but
   * no `X-Active-Org` — the invitation IS how they get one. PR 8
   * adds step-up at accept time.
   *
   * @returns OrgMemberResponse The new membership row.
   * @throws ApiError
   */
  public static acceptInvitation({
    token,
    idempotencyKey,
  }: {
    /**
     * Plaintext or base64url-encoded invitation token (same
     * shape as the GET /v1/invitations/{token} parameter).
     *
     */
    token: string,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<OrgMemberResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/invitations/{token}/accept',
      path: {
        'token': token,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      errors: {
        401: `code: unauthorized`,
        403: `\`403 Forbidden\` — the org is at the member cap. Stable
        code \`org_member_cap_exceeded\`.
        `,
        409: `\`409 Conflict\` — the bearer is already an active member
        of the target org. The wire detail carries the
        caller's current role. Stable code \`org_already_member\`.
        `,
        410: `\`410 Gone\` — the token is unknown, already consumed,
        already revoked, or expired. Stable codes
        \`org_invitation_invalid\` | \`org_invitation_expired\`.
        `,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Revoke a pending invitation.
   * Stamps `revoked_at` on a still-pending invitation via
   * `Store.RevokeOrgInvitation`. Owner + admin only
   * (`org.invite_members`, symmetric with the create-invite
   * path). Already-consumed / already-revoked / unknown IDs
   * collapse to a single `org_invitation_invalid` 410 (don't
   * leak which row state was reached). Emits
   * `org.invitation.revoked` with the stable invitation ID for dashboard
   * correlation. Plaintext tokens and hashes are never written to audit.
   *
   * @returns void
   * @throws ApiError
   */
  public static revokeInvitation({
    slug,
    invitationId,
  }: {
    /**
     * Org slug. Lowercase letters, digits, hyphens; must start
     * and end with alnum. 3..32 chars. Mirrors `OrgSlugPattern`
     * in `pkg/api/errors.go` exactly so the spec drift gate
     * (`make spec-check`) stays green.
     *
     */
    slug: string,
    /**
     * Stable invitation row ID returned by organization invitation list
     * responses. The URL is org-scoped, so an ID from another organization
     * is treated as an invalid invitation.
     *
     */
    invitationId: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/orgs/{slug}/invitations/{invitation_id}',
      path: {
        'slug': slug,
        'invitation_id': invitationId,
      },
      errors: {
        401: `code: unauthorized`,
        403: `\`403 Forbidden\` — caller lacks \`org.invite_members\`.
        Stable code \`org_role_forbidden\`.
        `,
        404: `code: not_found`,
        410: `\`410 Gone\` — the ID is unknown, already consumed,
        or already revoked. Stable code \`org_invitation_invalid\`.
        `,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Seat usage visibility for the active org.
   * Returns `{used, limit, plan}` from `Store.CountActiveOrgMembers`
   * (the same row the cap-in-tx inside `ConsumeOrgInvitation`
   * reads). `limit` comes from `org.Plan.OrgMembersMax()` — the
   * `free` plan returns `0` to render "personal org only" in the
   * dashboard rather than "0 of 0 used". Visibility-only; PR 9
   * ships the per-seat pricing cut-over per ADR-061 §"Out of
   * scope". Every role may read (gated by `org.view`).
   *
   * @returns SeatUsageResponse The seat usage.
   * @throws ApiError
   */
  public static getOrgSeatUsage({
    slug,
  }: {
    /**
     * Org slug. Lowercase letters, digits, hyphens; must start
     * and end with alnum. 3..32 chars. Mirrors `OrgSlugPattern`
     * in `pkg/api/errors.go` exactly so the spec drift gate
     * (`make spec-check`) stays green.
     *
     */
    slug: string,
  }): CancelablePromise<SeatUsageResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{slug}/seat_usage',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        403: `\`403 Forbidden\` — caller lacks \`org.view\`. Stable code
        \`org_role_forbidden\`.
        `,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * List org invitations (every state).
   * Cursor-paginated list of every invitation minted on the
   * org — pending, consumed, revoked, expired — in
   * `created_at DESC` order (id tiebreak). The cursor is a
   * opaque (base64-url-of-JSON) compound key `(created_at,
   * id)`; `?before=<cursor>` partitions the next page so
   * the row at the boundary is visited exactly once even
   * under random UUIDs (PR-9 cursor upgrade). Default limit
   * 25, max 100 (per the strict-mode pagination contract at
   * issue #393). Every role may read (gated by `org.view`,
   * the same access model as GET /v1/orgs/{slug}/members).
   * PR-8 ships the surface so the dashboard can render a
   * "Pending invitations" table next to the "Members"
   * table. Each render emits one `org.invitation.viewed`
   * audit row (success-only, per ADR-035).
   *
   * @returns InvitationListResponse The page of invitations.
   * @throws ApiError
   */
  public static listOrgInvitations({
    slug,
    before,
    limit = 25,
  }: {
    /**
     * Org slug. Lowercase letters, digits, hyphens; must start
     * and end with alnum. 3..32 chars. Mirrors `OrgSlugPattern`
     * in `pkg/api/errors.go` exactly so the spec drift gate
     * (`make spec-check`) stays green.
     *
     */
    slug: string,
    /**
     * Opaque cursor for the row immediately preceding the
     * next page. Base64-url encoding of a JSON object
     * `{"created_at":"...","id":"..."}` (the compound key,
     * encoded by the server in `next_before`). Pass the
     * prior response's `next_before` unchanged. Omit on
     * the first page. Malformed cursors return 400 with
     * the stable code `invalid_cursor` (PR-9).
     *
     */
    before?: string,
    /**
     * Max invitations to return per page. Silently capped at 100.
     */
    limit?: number,
  }): CancelablePromise<InvitationListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{slug}/invitations',
      path: {
        'slug': slug,
      },
      query: {
        'before': before,
        'limit': limit,
      },
      errors: {
        400: `\`400 Bad Request\` — \`?limit=\` is malformed, < 1, or
        > 100. Stable code \`validation_failed\`.
        `,
        401: `code: unauthorized`,
        403: `\`403 Forbidden\` — caller is not a member of the
        active org. Stable code \`org_role_forbidden\`.
        `,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
}
