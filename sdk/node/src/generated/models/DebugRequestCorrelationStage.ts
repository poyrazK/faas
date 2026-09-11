/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One bounded edge-to-billing request stage. Missing and partial signals are explicit and are never rendered as zero latency.
 */
export type DebugRequestCorrelationStage = {
  phase: 'edge' | 'queue' | 'wake' | 'guest' | 'downstream' | 'billing';
  status: 'observed' | 'partial' | 'missing' | 'not_applicable';
  started_at?: string;
  completed_at?: string;
  duration_ms?: number;
  evidence_count?: number;
  reason?: string;
  /**
   * True when the stage is derived from a collapsed request bucket rather than an exact request timestamp.
   */
  approximate?: boolean;
};

