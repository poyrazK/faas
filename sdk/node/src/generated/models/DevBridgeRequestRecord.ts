/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Redacted request metadata without queries, headers or bodies.
 */
export type DevBridgeRequestRecord = {
  id: number;
  started_at: string;
  method: string;
  path: string;
  status: number;
  duration_ms: number;
  response_bytes: number;
  complete: boolean;
  error?: string;
};

