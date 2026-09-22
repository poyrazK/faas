/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Single sample row returned by `GET /v1/apps/{slug}/errors/{fingerprint}/first`
 * (ADR-096 / PR-B). Embeds `AppErrorRequestItem` plus the
 * redacted `headers_sample` (jsonb-decoded to map) and the
 * `redactions_applied` pattern names so the dashboard can
 * render a "we redacted X / Y / Z" badge.
 *
 */
export type AppErrorSampleResponse = {
  request_id: string;
  received_at: string;
  route: string;
  http_status: number;
  error_class: string;
  sample_message: string;
  deployment_id?: string | null;
  instance_id?: string;
  node_id?: string;
  region?: string;
  commit_sha?: string;
  deployment_tag?: string;
  deployment_created_at?: string;
  image_digest?: string;
  /**
   * PII-redacted request headers (≤8 keys).
   */
  headers_sample: Record<string, string>;
  /**
   * Names of the redactor patterns that fired (e.g. `email`, `card`, `bearer`).
   */
  redactions_applied: Array<string>;
};

