/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Body for POST /v1/apps/{slug}/invoke[/async]. Method defaults to POST; path defaults to `/`.
 */
export type InvokeRequest = {
  payload?: Record<string, any>;
  headers?: Record<string, any>;
  method?: string;
  path?: string;
  /**
   * ADR-134 PR-B. Hard-stop timestamp. Must be within now+Limits.MaxAsyncInvocationDeadlineSeconds.
   */
  deadline_at?: string | null;
  /**
   * ADR-134 PR-B. Per-row retry curve override. Shape mirrors dispatch.RetryPolicy: { max_attempts, base_seconds, max_seconds, jitter_seconds }.
   */
  retry_policy?: any | null;
  /**
   * ADR-134 PR-B. Retention horizon in seconds. NULL/0 means 'use plan default' (Limits.MaxAsyncResultRetentionSeconds).
   */
  retention_seconds?: number | null;
};

