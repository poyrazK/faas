/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DebugEvidenceFinding } from './DebugEvidenceFinding.js';
import type { DebugEvidenceRecommendation } from './DebugEvidenceRecommendation.js';
import type { DebugEvidenceRef } from './DebugEvidenceRef.js';
import type { DebugTelemetrySpan } from './DebugTelemetrySpan.js';
/**
 * Bounded root-cause synthesis generated from the safe evidence payload.
 */
export type DebugEvidenceExplanation = {
  status: 'regression_detected' | 'unobserved' | 'regression_unavailable';
  headline: string;
  diagnosis?: 'request_failure' | 'performance_regression' | 'cold_start' | 'slow_path' | 'insufficient_evidence' | 'no_issue_observed';
  confidence?: 'high' | 'medium' | 'low';
  primary_span?: (DebugTelemetrySpan | null);
  findings?: Array<DebugEvidenceFinding>;
  recommendations?: Array<DebugEvidenceRecommendation>;
  evidence_refs?: Array<DebugEvidenceRef>;
  generated_by?: 'rules-v1';
};

