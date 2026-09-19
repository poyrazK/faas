/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DebugEvidenceExplanation } from './DebugEvidenceExplanation.js';
import type { DebugRegressionItem } from './DebugRegressionItem.js';
import type { DebugRequestCorrelation } from './DebugRequestCorrelation.js';
import type { DebugRequestCriticalPath } from './DebugRequestCriticalPath.js';
import type { DebugRequestDependencyLatency } from './DebugRequestDependencyLatency.js';
import type { DebugTelemetryRequestItem } from './DebugTelemetryRequestItem.js';
import type { DebugTelemetrySpan } from './DebugTelemetrySpan.js';
import type { DebugTimelineEvent } from './DebugTimelineEvent.js';
/**
 * Request metadata, deterministic wake/request timeline, bounded span evidence, matching regression, and explanation.
 */
export type DebugRequestEvidenceResponse = {
  request: DebugTelemetryRequestItem;
  regression?: (DebugRegressionItem | null);
  timeline: Array<DebugTimelineEvent>;
  correlation: DebugRequestCorrelation;
  critical_path?: DebugRequestCriticalPath;
  dependency_latency: Array<DebugRequestDependencyLatency>;
  /**
   * True when more than 16 dependency groups were retained.
   */
  dependency_latency_truncated: boolean;
  spans: Array<DebugTelemetrySpan>;
  spans_truncated: boolean;
  explanation: DebugEvidenceExplanation;
  generated_at: string;
};

