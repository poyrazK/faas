/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DebugTelemetrySpan } from './DebugTelemetrySpan.js';
/**
 * Deterministic explanation generated from the safe evidence payload.
 */
export type DebugEvidenceExplanation = {
  status: 'regression_detected' | 'unobserved';
  headline: string;
  primary_span?: (DebugTelemetrySpan | null);
};

