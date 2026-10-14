/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One log line attached to request evidence. The message is bounded to 512 bytes; structured log fields are not included.
 */
export type DebugEvidenceLogLine = {
  occurred_at: string;
  source: string;
  level?: string;
  stream?: string;
  message: string;
};

