/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CreateTriggerBatchRequest } from '../models/CreateTriggerBatchRequest.js';
import type { CreateTriggerBatchResponse } from '../models/CreateTriggerBatchResponse.js';
import type { CreateTriggerRequest } from '../models/CreateTriggerRequest.js';
import type { ListTriggerDeadLetterResponse } from '../models/ListTriggerDeadLetterResponse.js';
import type { ListTriggerRecordsResponse } from '../models/ListTriggerRecordsResponse.js';
import type { Trigger } from '../models/Trigger.js';
import type { TriggerDeadLetterReason } from '../models/TriggerDeadLetterReason.js';
import type { TriggerKind } from '../models/TriggerKind.js';
import type { TriggerMetricsResponse } from '../models/TriggerMetricsResponse.js';
import type { TriggerRecordState } from '../models/TriggerRecordState.js';
import type { UpdateTriggerRequest } from '../models/UpdateTriggerRequest.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class TriggersService {
  /**
   * List triggers on the account.
   * Returns every trigger owned by the calling account, optional
   * ?app_id filter to scope to one app, ?kind to scope to one
   * kind. Newest-first by created_at; result is unbounded but
   * the typical account has well under 200.
   *
   * @returns Trigger The trigger list.
   * @throws ApiError
   */
  public static listTriggers({
    appId,
    kind,
  }: {
    /**
     * Optional app UUID to scope the listing to one app; the
     * caller's account is still the authorization boundary.
     *
     */
    appId?: string,
    /**
     * Optional trigger kind filter (kafka / nats / redis_streams
     * / sqs_compat / queue). Invalid values produce an empty list,
     * not 400 — see the trigger list paginator in pkg/apid.
     *
     */
    kind?: TriggerKind,
  }): CancelablePromise<Array<Trigger>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/triggers',
      query: {
        'app_id': appId,
        'kind': kind,
      },
      errors: {
        401: `code: unauthorized`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Create a trigger.
   * Idempotent via Idempotency-Key header. Returns 402
   * `plan_triggers_not_allowed` for Free plan. A 403 may be
   * `trigger_kind_not_allowed`, `plan_trigger_quota`,
   * `trigger_batch_window_too_large`, or
   * `trigger_tls_skip_verify_not_allowed`; see ADR-100.
   *
   * @returns Trigger The new trigger.
   * @throws ApiError
   */
  public static createTrigger({
    requestBody,
    idempotencyKey,
  }: {
    requestBody: CreateTriggerRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<Trigger> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/triggers',
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: trigger_invalid_kind | trigger_invalid_config — kind does not exist, or per-kind validation failed (missing brokers, empty topic, malformed URL, etc.).`,
        401: `code: unauthorized`,
        402: `code: plan_triggers_not_allowed — Free plan cannot create triggers; upgrade required.`,
        403: `code: trigger_kind_not_allowed | plan_trigger_quota | trigger_batch_window_too_large | trigger_tls_skip_verify_not_allowed — source-kind or delivery configuration exceeds the account plan capabilities returned by GET /v1/account.`,
        422: `code: trigger_immutable_field — kind and (for cron) trigger_id are immutable after create; changing them requires delete + recreate.`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
        503: `code: secret_store_unavailable — Kafka credentials could not be sealed or opened safely because host age key material is unavailable.`,
      },
    });
  }
  /**
   * Bulk-create triggers from a gregale.yaml fragment.
   * Dashboard-only shortcut — fires a `triggers:` fragment at the
   * server, validates via the same path the CLI uses, and returns
   * per-row ids and any per-row RFC 7807 codes.
   *
   * @returns CreateTriggerBatchResponse Per-row outcome.
   * @throws ApiError
   */
  public static batchCreateTriggers({
    requestBody,
    idempotencyKey,
  }: {
    requestBody: CreateTriggerBatchRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<CreateTriggerBatchResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/triggers:batch_create',
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: trigger_invalid_kind | trigger_invalid_config — kind does not exist, or per-kind validation failed (missing brokers, empty topic, malformed URL, etc.).`,
        401: `code: unauthorized`,
        402: `code: plan_triggers_not_allowed — Free plan cannot create triggers; upgrade required.`,
        403: `code: trigger_kind_not_allowed | plan_trigger_quota | trigger_batch_window_too_large | trigger_tls_skip_verify_not_allowed — source-kind or delivery configuration exceeds the account plan capabilities returned by GET /v1/account.`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Get one trigger.
   * @returns Trigger The trigger.
   * @throws ApiError
   */
  public static getTrigger({
    id,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<Trigger> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/triggers/{id}',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Partial-update a trigger.
   * @returns Trigger The updated trigger.
   * @throws ApiError
   */
  public static updateTrigger({
    id,
    requestBody,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    requestBody: UpdateTriggerRequest,
  }): CancelablePromise<Trigger> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/triggers/{id}',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: trigger_invalid_kind | trigger_invalid_config — kind does not exist, or per-kind validation failed (missing brokers, empty topic, malformed URL, etc.).`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        422: `code: trigger_immutable_field — kind and (for cron) trigger_id are immutable after create; changing them requires delete + recreate.`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
        503: `code: secret_store_unavailable — Kafka credentials could not be sealed or opened safely because host age key material is unavailable.`,
      },
    });
  }
  /**
   * Delete a trigger.
   * @returns void
   * @throws ApiError
   */
  public static deleteTrigger({
    id,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/triggers/{id}',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Disable a trigger without deleting it.
   * Sets `enabled=false` and pg_notify's `trigger_changed`. Schedd
   * stops the broker poller on the next tick; in-flight records
   * drain normally.
   *
   * @returns void
   * @throws ApiError
   */
  public static pauseTrigger({
    id,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/triggers/{id}/pause',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Re-enable a paused trigger.
   * Sets `enabled=true` and pg_notify's `trigger_changed`.
   * Schedd restarts the broker poller on the next tick.
   *
   * @returns void
   * @throws ApiError
   */
  public static resumeTrigger({
    id,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/triggers/{id}/resume',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * List records for one trigger.
   * Newest-first by `received_at`. The dashboard uses this to
   * build the "Recent fires" view; the CLI uses it for `--tail`.
   *
   * @returns ListTriggerRecordsResponse Records for this trigger.
   * @throws ApiError
   */
  public static listTriggerRecords({
    id,
    state,
    limit = 50,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    /**
     * Optional state filter — pending / claimed / succeeded /
     * retry / dead_letter. Lets the dashboard narrow to
     * in-flight or DLQ rows.
     *
     */
    state?: TriggerRecordState,
    /**
     * Page size; default 50, capped at 200.
     *
     */
    limit?: number,
  }): CancelablePromise<ListTriggerRecordsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/triggers/{id}/records',
      path: {
        'id': id,
      },
      query: {
        'state': state,
        'limit': limit,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Force a record back into the dispatch queue.
   * Resets state to `pending`, attempts to 0. Operator-only scope
   * — the dashboard surfaces this as "Re-drive from DLQ".
   *
   * @returns void
   * @throws ApiError
   */
  public static retryTriggerRecord({
    id,
    rid,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    /**
     * Record id (UUID hex, no dashes) — used by retry/drop.
     */
    rid: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/triggers/{id}/records/{rid}/retry',
      path: {
        'id': id,
        'rid': rid,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `code: trigger_dlq_retry_failed — record could not be re-driven (state was not retry or dead_letter).`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Drop a record without re-firing.
   * Marks a dead-letter row `routed_to='drop'` (already the
   * default; this is the explicit acknowledgement). Operator-only
   * scope.
   *
   * @returns void
   * @throws ApiError
   */
  public static dropTriggerRecord({
    id,
    rid,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    /**
     * Record id (UUID hex, no dashes) — used by drop.
     */
    rid: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/triggers/{id}/records/{rid}/drop',
      path: {
        'id': id,
        'rid': rid,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * List dead-letter rows for one trigger.
   * @returns ListTriggerDeadLetterResponse Dead-letter rows for this trigger.
   * @throws ApiError
   */
  public static listTriggerDeadLetter({
    id,
    reason,
    limit = 50,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    /**
     * Optional DLQ reason filter (poison_record / max_attempts /
     * broker_error / rate_limited / payload_too_large).
     *
     */
    reason?: TriggerDeadLetterReason,
    /**
     * DLQ page size; default 50, capped at 200.
     *
     */
    limit?: number,
  }): CancelablePromise<ListTriggerDeadLetterResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/triggers/{id}/dlq',
      path: {
        'id': id,
      },
      query: {
        'reason': reason,
        'limit': limit,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Per-state aggregate counts for one trigger.
   * Counter roll-up by state. Not a Prometheus surface — that
   * is /v1/metrics (issue #684).
   *
   * @returns TriggerMetricsResponse Aggregated counts.
   * @throws ApiError
   */
  public static getTriggerMetrics({
    id,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<TriggerMetricsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/triggers/{id}/metrics',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Internal — schedd posts a batch envelope to the gateway.
   * Internal-only route. Schedd invokes this once per closed
   * batch (size / window / 6MB cap). The function under the
   * trigger responds with `{"batchItemFailures":[{"itemIdentifier":"..."}]}`.
   * Empty / missing response ⇒ full success. Mirrors AWS Lambda's
   * `ReportBatchItemFailures` contract verbatim.
   *
   * @returns any Batch accepted; per-record status derived from response.
   * @throws ApiError
   */
  public static dispatchInvocationBatch({
    requestBody,
  }: {
    requestBody: {
      trigger_id: string;
      app_id?: string;
      kind?: TriggerKind;
      records: Array<{
        item_identifier: string;
        payload_b64: string;
        headers?: Record<string, string>;
        metadata?: Record<string, any>;
      }>;
    },
  }): CancelablePromise<{
    succeeded?: Array<string>;
    retry?: Array<string>;
    dead_letter?: Array<string>;
  }> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/invocations:dispatch_batch',
      body: requestBody,
      mediaType: 'application/json',
    });
  }
}
