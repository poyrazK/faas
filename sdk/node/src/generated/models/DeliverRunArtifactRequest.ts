/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Exact Run output and stable storage intent to promote into an application upload route.
 */
export type DeliverRunArtifactRequest = {
  /**
   * Stable app/route delivery identity. Reuse after an uncertain response; a new key explicitly requests a new storage attempt without executing code.
   */
  idempotency_key: string;
  execution_id: string;
  artifact_name: string;
  size_bytes: number;
  sha256: string;
  content_type: string;
};

