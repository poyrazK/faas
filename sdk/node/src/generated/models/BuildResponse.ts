/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * DEPLOY-PROV-6 / ADR-089 (issue #741): the LIFECYCLE row for
 * a single build — current status, enqueued/started/finished
 * timestamps, failure_class, server-computed duration_seconds.
 * Companion to BuildProvenanceResponse (post-mortem export,
 * ADR-038) and the /sbom route (post-mortem blob, ADR-038
 * Phase 3). The status field mirrors builds.status — a
 * 5-state enum `queued|running|succeeded|failed|cancelled`.
 *
 * failure_class is the low-cardinality enum
 * `oom|timeout|user_error|infra` per the
 * `builds_failure_class_check` CHECK; present only when
 * status='failed'.
 *
 * duration_seconds is server-computed from started_at to the terminal
 * finished_at or cancelled_at timestamp; absent when the build never
 * started. CI scripts can
 * rely on its presence as "the build reached a terminal state
 * and elapsed N wall-clock seconds." error_message is
 * intentionally NOT in this response — it lives on
 * deployments; clients that need the per-failure string call
 * GET /v1/deployments/{id}.
 *
 */
export type BuildResponse = {
  id: string;
  deployment_id: string;
  kind: 'railpack' | 'dockerfile' | 'tarball' | 'github';
  source_bytes: number;
  status: 'queued' | 'running' | 'succeeded' | 'failed' | 'cancelled';
  failure_class?: 'oom' | 'timeout' | 'user_error' | 'infra';
  enqueued_at: string;
  started_at?: string;
  finished_at?: string;
  /**
   * Terminal cancellation timestamp. A queued cancellation can have cancelled_at without started_at or duration_seconds.
   */
  cancelled_at?: string;
  /**
   * Server-computed terminal timestamp (finished_at or cancelled_at) minus started_at in whole seconds. Absent when the build never started.
   */
  duration_seconds?: number;
  /**
   * Builderd cache decision for this build.
   */
  cache_status?: 'hit' | 'miss' | 'invalidated';
  /**
   * SHA-256 digest of the versioned BuildCacheRecipe.
   */
  cache_key_sha256?: string;
};

