/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DebugEvidenceExplanation } from './DebugEvidenceExplanation.js';
import type { DebugRegressionItem } from './DebugRegressionItem.js';
import type { DebugTelemetryRequestItem } from './DebugTelemetryRequestItem.js';
import type { DebugTelemetrySpan } from './DebugTelemetrySpan.js';
/**
 * Request metadata, bounded span evidence, matching regression, and explanation.
 */
export type DebugRequestEvidenceResponse = {
  request: DebugTelemetryRequestItem;
  regression?: (DebugRegressionItem | null);
  spans: Array<DebugTelemetrySpan>;
  spans_truncated: boolean;
  explanation: DebugEvidenceExplanation;
  generated_at: string;
};

