/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One row of the drill-down page (ADR-096 / PR-B).
 */
export type AppErrorRequestItem = {
  request_id: string;
  received_at: string;
  route: string;
  http_status: number;
  error_class: string;
  sample_message: string;
  /**
   * Nullable — the FK is ON DELETE SET NULL so an evicted deployment leaves the drill-down row intact.
   */
  deployment_id?: string | null;
  instance_id?: string;
  node_id?: string;
  region?: string;
  commit_sha?: string;
  deployment_tag?: string;
  deployment_created_at?: string;
  image_digest?: string;
};

