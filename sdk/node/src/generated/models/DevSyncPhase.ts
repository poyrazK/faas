/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Safe phase-level timing from a developer sync.
 */
export type DevSyncPhase = {
  phase: 'sync' | 'cache' | 'build' | 'boot' | 'ready' | 'route';
  status: 'completed' | 'failed' | 'in_progress';
  duration_ms?: number;
  reason?: string;
};

