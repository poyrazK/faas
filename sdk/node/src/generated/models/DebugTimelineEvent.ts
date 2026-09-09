/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Deterministic, bounded request and wake lifecycle marker. Raw event payloads are not returned.
 */
export type DebugTimelineEvent = {
  at: string;
  phase: 'request' | 'wake' | 'error' | 'regression';
  kind: string;
  actor?: string;
  summary: string;
  duration_ms?: number;
  status?: number;
  /**
   * True when the marker is derived from a collapsed minute bucket rather than an exact request timestamp.
   */
  approximate?: boolean;
};

