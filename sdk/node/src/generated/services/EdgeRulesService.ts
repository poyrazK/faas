/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CreateEdgeRuleListRequest } from '../models/CreateEdgeRuleListRequest.js';
import type { CreateEdgeRuleRequest } from '../models/CreateEdgeRuleRequest.js';
import type { CreateEnvironmentCachePurgeRequest } from '../models/CreateEnvironmentCachePurgeRequest.js';
import type { CreateEnvironmentCacheScheduleRequest } from '../models/CreateEnvironmentCacheScheduleRequest.js';
import type { DeploymentRoutePolicySnapshotResponse } from '../models/DeploymentRoutePolicySnapshotResponse.js';
import type { EdgeRuleEventsResponse } from '../models/EdgeRuleEventsResponse.js';
import type { EdgeRuleListResponse } from '../models/EdgeRuleListResponse.js';
import type { EdgeRuleResponse } from '../models/EdgeRuleResponse.js';
import type { EdgeRuleSetVersionResponse } from '../models/EdgeRuleSetVersionResponse.js';
import type { EdgeRuleStatsResponse } from '../models/EdgeRuleStatsResponse.js';
import type { EnvironmentCacheActivityRecovery } from '../models/EnvironmentCacheActivityRecovery.js';
import type { EnvironmentCacheConvergence } from '../models/EnvironmentCacheConvergence.js';
import type { EnvironmentCachePolicy } from '../models/EnvironmentCachePolicy.js';
import type { EnvironmentCachePolicyHistory } from '../models/EnvironmentCachePolicyHistory.js';
import type { EnvironmentCachePurgeReceipt } from '../models/EnvironmentCachePurgeReceipt.js';
import type { EnvironmentCachePurgeStatus } from '../models/EnvironmentCachePurgeStatus.js';
import type { EnvironmentCacheSchedule } from '../models/EnvironmentCacheSchedule.js';
import type { EnvironmentCacheScheduleActivity } from '../models/EnvironmentCacheScheduleActivity.js';
import type { EnvironmentCacheScheduleList } from '../models/EnvironmentCacheScheduleList.js';
import type { ListEdgeRuleListsResponse } from '../models/ListEdgeRuleListsResponse.js';
import type { RescheduleEnvironmentCacheRequest } from '../models/RescheduleEnvironmentCacheRequest.js';
import type { RollbackEdgeRulesRequest } from '../models/RollbackEdgeRulesRequest.js';
import type { SetEnvironmentCachePolicyRequest } from '../models/SetEnvironmentCachePolicyRequest.js';
import type { ThrottleSuggestionsResponse } from '../models/ThrottleSuggestionsResponse.js';
import type { UpdateEdgeRuleListRequest } from '../models/UpdateEdgeRuleListRequest.js';
import type { UpdateEdgeRuleRequest } from '../models/UpdateEdgeRuleRequest.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class EdgeRulesService {
  /**
   * Read the gateway policy captured with a deployment.
   * Returns the immutable edge-rule set captured atomically when this
   * deployment first became live. Rules use the same owner-scoped shape as
   * the current app edge-rule endpoint. Older deployments without a
   * snapshot return 404 so callers can report historical policy as unknown.
   *
   * @returns DeploymentRoutePolicySnapshotResponse Captured gateway route policy and fingerprint.
   * @throws ApiError
   */
  public static getDeploymentRoutePolicySnapshot({
    slug,
    deployment,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * UUID of the deployment whose gateway policy snapshot is requested.
     */
    deployment: string,
  }): CancelablePromise<DeploymentRoutePolicySnapshotResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/deployments/{deployment}/route-policy',
      path: {
        'slug': slug,
        'deployment': deployment,
      },
      errors: {
        401: `code: unauthorized`,
        404: `Deployment is not owned by the caller or has no captured route policy.`,
      },
    });
  }
  /**
   * Capture an environment cache schedule activity checkpoint before baseline discovery.
   * Requires app read access and MFA. Reads the retained-prefix floor and
   * committed head in one scoped snapshot without scanning or consuming
   * events. Returns nonempty oldest_cursor and restart_cursor even for an
   * empty feed or a fully pruned ledger. Save restart_cursor BEFORE listing
   * current schedules, then consume activity after it to cover concurrent
   * changes. The checkpoint does not reserve retained data, freeze discovery,
   * restore removed history or establish gateway convergence. Recheck cursor
   * retention while recovering; an expired recovery must restart its baseline.
   *
   * @returns EnvironmentCacheActivityRecovery Immutable target identity, retained-prefix checkpoint and observed head.
   * @throws ApiError
   */
  public static getEnvironmentCacheScheduleActivityCheckpoint({
    slug,
    environment,
  }: {
    /**
     * App owning the feed checkpoint captured for baseline recovery.
     */
    slug: string,
    /**
     * Named environment whose retained bounds and committed head are captured.
     */
    environment: string,
  }): CancelablePromise<EnvironmentCacheActivityRecovery> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/cache/schedules/activity/checkpoint',
      path: {
        'slug': slug,
      },
      query: {
        'environment': environment,
      },
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Catch up on durable environment cache schedule transitions.
   * Requires app read access and MFA. Records accepted, rescheduled, cancelled,
   * executed and failed transitions in the same transaction as the schedule
   * change. A per-app/environment transactional counter serializes sequence
   * allocation through commit, so cursors cannot skip a late transaction.
   * Rescheduling emits the cancelled original with its successor ID followed
   * by acceptance of the successor linked to its predecessor. Identical retries
   * and stale worker completions emit no additional events. Claim/lease retries
   * are not outcomes and emit no events. Execution includes only the policy
   * revision or purge generation receipt; gateway convergence is separate.
   * Cursors bind account and immutable app/project/environment IDs. Rejects
   * malformed, foreign and ahead-of-stream checkpoints. Entries are ascending
   * and contiguous; cursor always identifies the last returned sequence, or
   * the supplied checkpoint for an empty page. Persist it even at the tail
   * and supply it again after reconnecting. Has_more means another committed
   * event was visible in the same snapshot. Events retain compact metadata,
   * timestamps and stable failure codes, without rule bodies or paths. They
   * are retained for 30 days plus bounded cleanup lag. An expired checkpoint
   * returns 410 cursor_expired with scoped activity_recovery metadata. Save
   * restart_cursor before listing a current schedule baseline, then consume
   * events after it and reconcile overlapping schedule identities. Removed
   * historical outcomes cannot be reconstructed. Recording begins with upgraded apid writers;
   * no historical events are fabricated. Use schedule listing for the initial
   * baseline and enable catch-up only after all apid writers are upgraded.
   *
   * @returns EnvironmentCacheScheduleActivity A bounded ordered activity page with a durable tail-safe checkpoint.
   * @throws ApiError
   */
  public static getEnvironmentCacheScheduleActivity({
    slug,
    environment,
    after,
    limit = 20,
  }: {
    /**
     * App whose scoped durable schedule transitions are being read.
     */
    slug: string,
    /**
     * Named environment authorizing this immutable schedule activity stream.
     */
    environment: string,
    /**
     * Saved activity cursor from the same account and immutable environment.
     */
    after?: string,
    /**
     * Maximum compact schedule events to return in this activity page.
     */
    limit?: number,
  }): CancelablePromise<EnvironmentCacheScheduleActivity> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/cache/schedules/activity',
      path: {
        'slug': slug,
      },
      query: {
        'environment': environment,
        'after': after,
        'limit': limit,
      },
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        410: `The checkpoint precedes the retained event prefix; use activity_recovery to rebuild current state.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Discover named environment cache schedules with bounded cursor pages.
   * Requires app read access and MFA. Returns schedules for the immutable
   * environment in ascending execute_at, schedule_id order, including linked
   * replacements. Optional status, inclusive from and exclusive until filters
   * are bound into the opaque continuation cursor along with immutable target
   * IDs. Default page size 20, maximum 50. Pages are live, not a frozen snapshot;
   * status changes and new replacements may require restarting from page one.
   * Schedule execution receipts are distinct from gateway convergence evidence.
   *
   * @returns EnvironmentCacheScheduleList A bounded scoped schedule page and optional continuation cursor.
   * @throws ApiError
   */
  public static listEnvironmentCacheSchedules({
    slug,
    environment,
    status,
    from,
    until,
    after,
    limit = 20,
  }: {
    /**
     * App owning this one-time cache schedule target.
     */
    slug: string,
    /**
     * Named environment granting access to the scheduled cache action.
     */
    environment: string,
    /**
     * Restrict this discovery page to the selected execution state.
     */
    status?: 'scheduled' | 'executing' | 'executed' | 'failed' | 'cancelled',
    /**
     * Include schedules with execution timestamps at or after this bound.
     */
    from?: string,
    /**
     * Include schedules with execution timestamps strictly before this bound.
     */
    until?: string,
    /**
     * Opaque next_cursor from the same immutable environment and filter selection.
     */
    after?: string,
    /**
     * Maximum schedule records in this live discovery page.
     */
    limit?: number,
  }): CancelablePromise<EnvironmentCacheScheduleList> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/cache/schedules',
      path: {
        'slug': slug,
      },
      query: {
        'environment': environment,
        'status': status,
        'from': from,
        'until': until,
        'after': after,
        'limit': limit,
      },
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Accept one future environment cache action.
   * Requires app write access and MFA. Delegates exactly one policy replacement,
   * inheritance restore or purge to apid at or after execute_at, within 30 days
   * of acceptance. At most 100 scheduled/executing actions per app/environment.
   * Schedule UUID doubles as the action request UUID; immutable IDs and complete
   * action are required. Policy pins expected_revision both at acceptance and
   * execution; copy_from and restore_revision are unsupported. Purge requires
   * explicit path, including empty string for the whole environment host.
   * Identical retries recover the same schedule even after execution time or
   * terminal completion. Schedule acceptance is not action execution or gateway
   * convergence. Durable delegation survives client disconnect and token expiry;
   * cancel explicitly before worker claim. Delayed actions execute after outage
   * recovery; execution time is a lower bound, not a real-time deadline.
   *
   * @returns EnvironmentCacheSchedule Durable one-time cache schedule or original retry receipt.
   * @throws ApiError
   */
  public static createEnvironmentCacheSchedule({
    slug,
    environment,
    requestBody,
  }: {
    /**
     * App owning this one-time cache schedule target.
     */
    slug: string,
    /**
     * Named environment granting access to the scheduled cache action.
     */
    environment: string,
    requestBody: CreateEnvironmentCacheScheduleRequest,
  }): CancelablePromise<EnvironmentCacheSchedule> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/cache/schedules',
      path: {
        'slug': slug,
      },
      query: {
        'environment': environment,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Read one environment cache schedule and execution receipt.
   * Requires app read access and MFA. Returns scheduled, executing, executed,
   * failed or cancelled. Executed includes the exact policy revision or purge
   * generation receipt; it does not establish gateway convergence. Use policy
   * convergence or purge status for current-session registered gateway evidence.
   * Immutable environment identity is rechecked; no cross-environment access.
   *
   * @returns EnvironmentCacheSchedule Exact scoped schedule with its current execution state.
   * @throws ApiError
   */
  public static getEnvironmentCacheSchedule({
    slug,
    environment,
    schedule,
  }: {
    /**
     * App associated with the exact schedule being inspected.
     */
    slug: string,
    /**
     * Registered named environment owning the selected schedule.
     */
    environment: string,
    /**
     * Exact immutable schedule UUID returned at acceptance.
     */
    schedule: string,
  }): CancelablePromise<EnvironmentCacheSchedule> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/cache/schedules/{schedule}',
      path: {
        'slug': slug,
        'schedule': schedule,
      },
      query: {
        'environment': environment,
      },
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Cancel a cache schedule before worker claim.
   * Requires app write access and MFA. Atomically cancels a scheduled action;
   * repeated cancellation returns the cancelled record. Executing and terminal
   * actions reject cancellation with conflict. Cancellation does not reverse an
   * already applied policy or purge. Cancellation remains available for owners
   * of suspended accounts so pending delegation can be withdrawn.
   *
   * @returns EnvironmentCacheSchedule Confirmed cancelled cache schedule including on repeated cancellation.
   * @throws ApiError
   */
  public static cancelEnvironmentCacheSchedule({
    slug,
    environment,
    schedule,
  }: {
    /**
     * App associated with the exact schedule being inspected.
     */
    slug: string,
    /**
     * Registered named environment owning the selected schedule.
     */
    environment: string,
    /**
     * Exact immutable schedule UUID returned at acceptance.
     */
    schedule: string,
  }): CancelablePromise<EnvironmentCacheSchedule> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/cache/schedules/{schedule}',
      path: {
        'slug': slug,
        'schedule': schedule,
      },
      query: {
        'environment': environment,
      },
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Atomically replace a pending cache schedule at a new execution time.
   * Requires app write access, account eligibility and MFA. Pins the original
   * immutable target and execute_at via expected_execute_at. Cancels only a
   * scheduled original and inserts a linked successor in one transaction,
   * serialized against worker claim and cancellation. New time must differ and
   * be future within 30 days. Preserves the complete action and original policy
   * revision guard; a changed desired policy rejects replacement rather than
   * rebasing it. Replacement consumes the original active quota slot. The new
   * UUID also becomes the action idempotency key. Identical retries recover the
   * successor even after terminal state, policy edits or further replacement;
   * conflicting UUID or changed body rejects without cancelling the original.
   * Worker-claimed and terminal originals cannot be newly rescheduled. The
   * cancelled original links replaced_by_schedule_id; the successor links
   * replaces_schedule_id. Cancel the successor to withdraw the new delegation;
   * cancelling the original does not cancel its successor. Acceptance does not
   * execute a cache action or establish gateway convergence.
   *
   * @returns EnvironmentCacheSchedule Confirmed linked successor or its recovered original replacement receipt.
   * @throws ApiError
   */
  public static rescheduleEnvironmentCache({
    slug,
    schedule,
    environment,
    requestBody,
  }: {
    /**
     * App owning the pending schedule selected for atomic replacement.
     */
    slug: string,
    /**
     * Original pending schedule UUID whose action is preserved.
     */
    schedule: string,
    /**
     * Exact registered environment authorizing the schedule replacement.
     */
    environment: string,
    requestBody: RescheduleEnvironmentCacheRequest,
  }): CancelablePromise<EnvironmentCacheSchedule> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/cache/schedules/{schedule}/reschedule',
      path: {
        'slug': slug,
        'schedule': schedule,
      },
      query: {
        'environment': environment,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Request an exact-path or whole-environment logical cache purge.
   * Requires app write access and MFA. Immutable app/project/environment IDs
   * and request_id are required. Path must be explicitly supplied: empty
   * string targets the entire immutable environment host, otherwise an exact
   * decoded URL path without query, fragment, percent escapes, wildcards,
   * controls or dot segments. All query, method, rule and vary variants at
   * that path are invalidated; other environment hosts and policy are unchanged.
   * A commit-ordered durable generation and retry receipt are the same row.
   * Retry the identical request_id and body after a lost reply; changed target
   * reuse returns conflict. New gateways bypass environment caching until
   * replayed; prior-generation L1/L2 entries and late cache writes become
   * unreachable. Redis objects expire by TTL rather than physical deletion.
   * The receipt confirms desired intent. Read purge status for current-session
   * gateway acknowledgement; completion is sampled registered-fleet evidence,
   * not an in-flight response drain or application-data freshness guarantee.
   *
   * @returns EnvironmentCachePurgeReceipt Stable committed purge request, including on idempotent replay.
   * @throws ApiError
   */
  public static createEnvironmentCachePurge({
    slug,
    environment,
    requestBody,
  }: {
    /**
     * App whose immutable environment-host cache is being invalidated.
     */
    slug: string,
    /**
     * Registered named environment targeted by this durable purge request.
     */
    environment: string,
    requestBody: CreateEnvironmentCachePurgeRequest,
  }): CancelablePromise<EnvironmentCachePurgeReceipt> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/cache/purges',
      path: {
        'slug': slug,
      },
      query: {
        'environment': environment,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Read a scoped cache purge receipt and gateway completion evidence.
   * Requires app read access and MFA. Returns completed only when every
   * registered active serving compute gateway has replayed this generation
   * in its current process session with fresh node and session heartbeats
   * within 90 seconds. Fresh sessions without the checkpoint are pending;
   * missing/stale sessions, changed endpoints and empty fleets are degraded.
   * Reads identity, receipt and aggregate counts from one snapshot and
   * rechecks the environment registry. Recreated environments cannot read
   * old receipts. No gateway names, addresses or unrelated cache data are
   * returned. Read-only; a later restart or roster change may change status.
   *
   * @returns EnvironmentCachePurgeStatus Exact purge receipt and sampled registered-gateway status.
   * @throws ApiError
   */
  public static getEnvironmentCachePurge({
    slug,
    request,
    environment,
  }: {
    /**
     * App owning the requested environment purge receipt.
     */
    slug: string,
    /**
     * Exact durable purge request UUID supplied at creation.
     */
    request: string,
    /**
     * Registered environment owning this exact cache purge request.
     */
    environment: string,
  }): CancelablePromise<EnvironmentCachePurgeStatus> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/cache/purges/{request}',
      path: {
        'slug': slug,
        'request': request,
      },
      query: {
        'environment': environment,
      },
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Read a bounded page of environment cache policy revisions.
   * Requires app read access and MFA. Returns retained snapshots in descending
   * revision order for the exact immutable environment. before_revision is an
   * exclusive cursor; zero starts at newest. Includes synthetic initial
   * inherited revision zero, which does not contain historical app rule bodies.
   * Current revision and entries share one read-only snapshot. New writes
   * record history atomically with intent and receipts; receipts/current rows
   * available at migration are backfilled. Missing older revisions are not
   * fabricated. Deleting an app or environment removes its history.
   *
   * @returns EnvironmentCachePolicyHistory Newest-first revision page and optional next exclusive cursor.
   * @throws ApiError
   */
  public static listEnvironmentCachePolicyHistory({
    slug,
    environment,
    beforeRevision,
    limit = 20,
  }: {
    /**
     * App whose cache policy history is being paged.
     */
    slug: string,
    /**
     * Named environment whose retained cache revisions are listed.
     */
    environment: string,
    /**
     * Exclusive revision cursor, or zero to start at newest.
     */
    beforeRevision?: number,
    /**
     * Maximum snapshots returned in this page.
     */
    limit?: number,
  }): CancelablePromise<EnvironmentCachePolicyHistory> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/cache-policy/history',
      path: {
        'slug': slug,
      },
      query: {
        'environment': environment,
        'before_revision': beforeRevision,
        'limit': limit,
      },
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Read one retained environment cache policy revision.
   * Requires app read access and MFA. Returns 404 for unavailable positive revisions. Revision zero is synthetic initial live inheritance, not historical app rule bodies. Identity and history are read from one snapshot; environment replacement cannot recover old snapshots.
   * @returns EnvironmentCachePolicy Exact historical cache policy snapshot.
   * @throws ApiError
   */
  public static getEnvironmentCachePolicyRevision({
    slug,
    revision,
    environment,
  }: {
    /**
     * App owning the requested historical cache snapshot.
     */
    slug: string,
    /**
     * Exact retained revision; zero selects initial live inheritance.
     */
    revision: number,
    /**
     * Named environment owning this exact cache revision.
     */
    environment: string,
  }): CancelablePromise<EnvironmentCachePolicy> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/cache-policy/history/{revision}',
      path: {
        'slug': slug,
        'revision': revision,
      },
      query: {
        'environment': environment,
      },
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Read current-session gateway evidence for an environment cache policy.
   * Counts registered active compute gateways with a nonempty serving URL.
   * Converged requires every gateway's current session to have loaded the
   * exact desired revision, with session and node heartbeats fresh within
   * 90 seconds. Empty or stale rosters are degraded, never successful.
   * Process restart or endpoint replacement invalidates previous-session
   * observations. Counts describe sampled evidence, not continuous serving
   * behavior or cache savings. Inherited revision zero is degraded because
   * no independent environment cache revision exists to acknowledge.
   *
   * @returns EnvironmentCacheConvergence Scoped desired revision and sampled fleet observations.
   * @throws ApiError
   */
  public static getEnvironmentCacheConvergence({
    slug,
    environment,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Registered named environment slug.
     */
    environment: string,
  }): CancelablePromise<EnvironmentCacheConvergence> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/cache-policy/convergence',
      path: {
        'slug': slug,
      },
      query: {
        'environment': environment,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Read a named environment's independent cache policy.
   * An absent override reports inherited=true and no inherited rule bodies. Existing app-owned cache rules continue to apply until an explicit replacement is saved.
   * @returns EnvironmentCachePolicy Desired cache policy for the exact immutable environment identity.
   * @throws ApiError
   */
  public static getEnvironmentCachePolicy({
    slug,
    environment,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Registered named environment slug. Default scope is unsupported.
     */
    environment: string,
  }): CancelablePromise<EnvironmentCachePolicy> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/cache-policy',
      path: {
        'slug': slug,
      },
      query: {
        'environment': environment,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Replace environment cache rules or restore app inheritance.
   * Replaces only cache rules for the selected immutable environment host.
   * An explicit empty list disables caching there. No other environment,
   * default host, rule kind or app-owned rule is mutated. Native account,
   * app and registry checks and the expected revision guard are atomic
   * with a durable request_id receipt. Retry the identical request_id and
   * body after an ambiguous response; conflicting key reuse returns 409.
   * A deleted and recreated environment never inherits an old receipt.
   * Gateways reload desired rules on their normal 30-second policy TTL.
   * Revision-scoped rule IDs partition response-cache entries after reload.
   * The receipt describes desired policy, not gateway convergence. Set
   * inherited=true with rules=[] to restore live app inheritance at a new
   * positive revision. The inherited rules are evaluated on gateway reload;
   * the review does not freeze their contents. Omitted/false inherited with
   * rules=[] still disables caching. Restoring inheritance partitions old
   * response entries and is observable by the convergence endpoint.
   * Optional copy_from pins an explicit override on a different named
   * environment of the same app/project. Its immutable identity, revision
   * and normalized rules are checked in the destination write transaction.
   * Inherited sources are rejected; an explicit empty source disables cache
   * at the destination. Source policy is unchanged; this is a one-time copy.
   * Source identity is checked on retries, while an existing receipt can
   * recover after source or destination revision changes.
   * Optional restore_revision restores a retained policy snapshot as a new
   * revision. The inherited mode and rules must match that snapshot in the
   * same guarded write transaction; zero selects initial live inheritance.
   * It cannot be combined with copy_from. Intent, history and the retry
   * receipt commit atomically. A rollback never decrements the current
   * revision or overwrites a concurrent edit.
   *
   * @returns EnvironmentCachePolicy Saved desired policy, or the original transactional receipt on retry.
   * @throws ApiError
   */
  public static setEnvironmentCachePolicy({
    slug,
    environment,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Registered named environment slug. Default scope is unsupported.
     */
    environment: string,
    requestBody: SetEnvironmentCachePolicyRequest,
  }): CancelablePromise<EnvironmentCachePolicy> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/cache-policy',
      path: {
        'slug': slug,
      },
      query: {
        'environment': environment,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * List every edge rule owned by the caller.
   * Account-wide listing. The dashboard overview pane uses this;
   * the CLI uses it for `gregale edge-rules list`. Free plans only
   * see rule kinds their plan unlocks — the server still lists
   * them.
   *
   * @returns EdgeRuleResponse Every edge rule owned by the caller.
   * @throws ApiError
   */
  public static listEdgeRules(): CancelablePromise<Array<EdgeRuleResponse>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/edge-rules',
      errors: {
        401: `code: unauthorized`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * List the account's reusable edge-rule lists.
   * ADR-963. Items are omitted here; fetch one list for its items.
   * referenced_by names the rules whose match conditions use the list.
   *
   * @returns ListEdgeRuleListsResponse The account's lists, by name.
   * @throws ApiError
   */
  public static listEdgeRuleLists(): CancelablePromise<ListEdgeRuleListsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/edge-rule-lists',
      errors: {
        401: `code: unauthorized`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Create a reusable edge-rule list.
   * ADR-963. Items are validated for the list kind and stored
   * canonicalized, deduplicated and sorted. Lists per account and items
   * per list are plan limits.
   *
   * @returns EdgeRuleListResponse The created list.
   * @throws ApiError
   */
  public static createEdgeRuleList({
    requestBody,
  }: {
    requestBody: CreateEdgeRuleListRequest,
  }): CancelablePromise<EdgeRuleListResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/edge-rule-lists',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: plan_limit_edge_rule_lists | plan_limit_edge_rule_list_items`,
        409: `code: edge_rule_list_exists`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Get one edge-rule list with its items.
   * @returns EdgeRuleListResponse The list.
   * @throws ApiError
   */
  public static getEdgeRuleList({
    name,
  }: {
    /**
     * Edge-rule list name, unique within the account.
     */
    name: string,
  }): CancelablePromise<EdgeRuleListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/edge-rule-lists/{name}',
      path: {
        'name': name,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: edge_rule_list_not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Edit an edge-rule list.
   * ADR-963. items replaces the list; add and remove edit it in place
   * (remove applies after add) and cannot be combined with items.
   * Gateways pick up the change for every referencing rule within a few
   * seconds; no rule-set version is recorded.
   *
   * @returns EdgeRuleListResponse The updated list.
   * @throws ApiError
   */
  public static updateEdgeRuleList({
    name,
    requestBody,
  }: {
    /**
     * Edge-rule list name, unique within the account.
     */
    name: string,
    requestBody: UpdateEdgeRuleListRequest,
  }): CancelablePromise<EdgeRuleListResponse> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/edge-rule-lists/{name}',
      path: {
        'name': name,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: plan_limit_edge_rule_list_items`,
        404: `code: edge_rule_list_not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Delete an unreferenced edge-rule list.
   * @returns void
   * @throws ApiError
   */
  public static deleteEdgeRuleList({
    name,
  }: {
    /**
     * Edge-rule list name, unique within the account.
     */
    name: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/edge-rule-lists/{name}',
      path: {
        'name': name,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: edge_rule_list_not_found`,
        409: `code: edge_rule_list_in_use (rules still reference the list)`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * List every edge rule bound to one app.
   * @returns EdgeRuleResponse Edge rules for this app, ordered by priority ASC.
   * @throws ApiError
   */
  public static listEdgeRulesForApp({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<Array<EdgeRuleResponse>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/edge-rules',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Create an edge rule on an app.
   * Kind is one of {route, rewrite, redirect, headers, cors, jwt, ip,
   * validate, geo, async}. `action` is a kind-tagged jsonb body — the per-kind
   * shape is documented under components/schemas. Plan-kind gate:
   * jwt/ip return 402 plan_edge_rule_kind_not_allowed on Free; geo
   * is allowed on Free with a tighter per-app quota. Per-app
   * quota returns 402 plan_limit_edge_rules once EdgeRulesPerApp
   * is reached.
   *
   * @returns EdgeRuleResponse Rule created.
   * @throws ApiError
   */
  public static createEdgeRule({
    slug,
    requestBody,
    ifMatch,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: CreateEdgeRuleRequest,
    /**
     * Optional edge-rule set version (the ETag of the app's rule listing). The mutation is refused with 412 when it is no longer the app's latest version.
     */
    ifMatch?: string,
  }): CancelablePromise<EdgeRuleResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/edge-rules',
      path: {
        'slug': slug,
      },
      headers: {
        'If-Match': ifMatch,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `Plan-kind or per-app quota rejected the rule.`,
        404: `code: not_found`,
        409: `code: edge_rule_conflict — duplicate or overlapping rule state rejected.`,
        412: `If-Match no longer names the app's latest edge-rule set version (edge_rules_version_mismatch). The response ETag carries the current version.`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Fetch one edge rule by id.
   * @returns EdgeRuleResponse The edge rule.
   * @throws ApiError
   */
  public static getEdgeRule({
    id,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<EdgeRuleResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/edge-rules/{id}',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Partial-update an edge rule.
   * Every field is optional. `kind` is NOT patchable — rotating
   * kind mid-life would break the action union. To change kind,
   * delete and recreate. `action` replaces the jsonb column
   * whole (no partial-update shape).
   * PATCH accepts an optional Idempotency-Key with the standard 24-hour
   * replay lifetime. Retry the same method, path, body, If-Match and key
   * after a lost response. A stored success is replayed before checking
   * the now-advanced version; it describes the original write, not current
   * policy or gateway convergence. If a supplied version cannot be checked,
   * the mutation fails closed.
   *
   * @returns EdgeRuleResponse Updated rule.
   * @throws ApiError
   */
  public static updateEdgeRule({
    id,
    requestBody,
    ifMatch,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    requestBody: UpdateEdgeRuleRequest,
    /**
     * Optional edge-rule set version (the ETag of the app's rule listing). The mutation is refused with 412 when it is no longer the app's latest version.
     */
    ifMatch?: string,
  }): CancelablePromise<EdgeRuleResponse> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/edge-rules/{id}',
      path: {
        'id': id,
      },
      headers: {
        'If-Match': ifMatch,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        412: `If-Match no longer names the app's latest edge-rule set version (edge_rules_version_mismatch). The response ETag carries the current version.`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Delete an edge rule.
   * @returns void
   * @throws ApiError
   */
  public static deleteEdgeRule({
    id,
    ifMatch,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    /**
     * Optional edge-rule set version (the ETag of the app's rule listing). The mutation is refused with 412 when it is no longer the app's latest version.
     */
    ifMatch?: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/edge-rules/{id}',
      path: {
        'id': id,
      },
      headers: {
        'If-Match': ifMatch,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        412: `If-Match no longer names the app's latest edge-rule set version (edge_rules_version_mismatch). The response ETag carries the current version.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * List recorded versions of an app's edge-rule set, newest first.
   * Every committed change to an app's edge rules records the whole rule
   * set as a new version (ADR-961). Up to the 50 newest versions are
   * returned, without rule bodies; the newest 100 are retained.
   *
   * @returns EdgeRuleSetVersionResponse Versions, newest first.
   * @throws ApiError
   */
  public static listEdgeRuleSetVersions({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<Array<EdgeRuleSetVersionResponse>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/edge-rules/versions',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Fetch one recorded edge-rule set version with its rules.
   * @returns EdgeRuleSetVersionResponse The version and the rules it recorded.
   * @throws ApiError
   */
  public static getEdgeRuleSetVersion({
    slug,
    version,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Rule-set version number, from the versions listing.
     */
    version: number,
  }): CancelablePromise<EdgeRuleSetVersionResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/edge-rules/versions/{version}',
      path: {
        'slug': slug,
        'version': version,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Sampled requests each edge rule matched, newest first.
   * ADR-964. Gateways keep the first 10 matches of each rule per minute
   * as full events (request ID, method, host, path without query string,
   * trusted client IP, country, user agent); hit counts (stats) cover
   * every match. Events are kept 7 days; how far back a plan can read is
   * a plan limit (Free 24 h, Hobby 72 h, Pro and Scale 7 days), and a
   * longer since is clamped (the response reports the effective start).
   *
   * @returns EdgeRuleEventsResponse One page of events.
   * @throws ApiError
   */
  public static listEdgeRuleEvents({
    slug,
    rule,
    outcome,
    since = '24h',
    limit = 50,
    cursor,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Only events for this edge rule id.
     */
    rule?: string,
    /**
     * Only events with this outcome; detected, warned and blocked are kind=waf detections.
     */
    outcome?: 'matched' | 'logged' | 'detected' | 'warned' | 'blocked',
    /**
     * Duration such as 1h, 24h or 7d.
     */
    since?: string,
    /**
     * Events per page.
     */
    limit?: number,
    /**
     * next_cursor from the previous page.
     */
    cursor?: string,
  }): CancelablePromise<EdgeRuleEventsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/edge-rules/events',
      path: {
        'slug': slug,
      },
      query: {
        'rule': rule,
        'outcome': outcome,
        'since': since,
        'limit': limit,
        'cursor': cursor,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Per-rule match counts for an app over a window.
   * ADR-960. Gateways count each rule's matches (matched for enforced
   * rules, logged for log-mode rules), at most once per rule per request,
   * and flush them into hourly buckets once a minute; buckets are kept
   * for 14 days. Rules with no matches in the window are omitted.
   *
   * @returns EdgeRuleStatsResponse Per-rule counts.
   * @throws ApiError
   */
  public static getEdgeRuleStats({
    slug,
    window = '24h',
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * How far back to total hit counts.
     */
    window?: '1h' | '24h' | '7d',
  }): CancelablePromise<EdgeRuleStatsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/edge-rules/stats',
      path: {
        'slug': slug,
      },
      query: {
        'window': window,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Restore an app's edge rules to a recorded version.
   * Replaces every current rule with the version's rules in one
   * transaction, preserving rule IDs, behind fleet convergence. The
   * restore is recorded as a new version. Refused when the version
   * exceeds the current plan's edge-rule quotas or references a deleted
   * CORS preset.
   *
   * @returns EdgeRuleResponse The rules now in force.
   * @throws ApiError
   */
  public static rollbackEdgeRules({
    slug,
    requestBody,
    ifMatch,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: RollbackEdgeRulesRequest,
    /**
     * Optional edge-rule set version (the ETag of the app's rule listing). The mutation is refused with 412 when it is no longer the app's latest version.
     */
    ifMatch?: string,
  }): CancelablePromise<Array<EdgeRuleResponse>> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/edge-rules/rollback',
      path: {
        'slug': slug,
      },
      headers: {
        'If-Match': ifMatch,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `The version references a CORS preset that no longer exists.`,
        412: `If-Match no longer names the app's latest edge-rule set version (edge_rules_version_mismatch). The response ETag carries the current version.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Per-route throttle recommender (ADR-091 D20.5 amendment,
   * issue #881). Read-only: returns a suggested rps/burst per
   * route over the window, clamped to the customer's plan
   * ceiling so the suggestion is always settable.
   *
   * The recommender is ADVICE-ONLY — it never auto-applies.
   * Customers confirm via POST /v1/apps/{slug}/edge-rules.
   *
   * @returns ThrottleSuggestionsResponse Suggestion payload. Source is `prometheus` on success
   * or `degraded: <reason>` on Prometheus failure
   * (response is still 200 with empty Suggestions — the
   * dashboard's empty-state branch handles it).
   *
   * @throws ApiError
   */
  public static getAppThrottleSuggestions({
    slug,
    range = '5m',
  }: {
    /**
     * App slug (lowercase, kebab-case; per-account unique). The recommender walks the per-route rate() for this app's gatewayd-internal reader.
     */
    slug: string,
    /**
     * Prometheus window. Closed vocabulary (see
     * `pkg/appmetrics.Ranges`). Defaults to 5m.
     *
     */
    range?: string,
  }): CancelablePromise<ThrottleSuggestionsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/throttle-suggestions',
      path: {
        'slug': slug,
      },
      query: {
        'range': range,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
}
