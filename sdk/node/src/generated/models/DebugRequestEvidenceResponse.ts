/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DebugDependencyDeploymentComparison } from './DebugDependencyDeploymentComparison.js';
import type { DebugEvidenceExplanation } from './DebugEvidenceExplanation.js';
import type { DebugEvidenceLogLine } from './DebugEvidenceLogLine.js';
import type { DebugRegressionItem } from './DebugRegressionItem.js';
import type { DebugRequestCorrelation } from './DebugRequestCorrelation.js';
import type { DebugRequestCriticalPath } from './DebugRequestCriticalPath.js';
import type { DebugRequestDependencyLatency } from './DebugRequestDependencyLatency.js';
import type { DebugTelemetryRequestItem } from './DebugTelemetryRequestItem.js';
import type { DebugTelemetrySpan } from './DebugTelemetrySpan.js';
import type { DebugTimelineEvent } from './DebugTimelineEvent.js';
/**
 * Request metadata, deterministic wake/request timeline, bounded span evidence, the request's own log lines, matching regression, and explanation.
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
  dependency_comparison?: DebugDependencyDeploymentComparison;
  spans: Array<DebugTelemetrySpan>;
  spans_truncated: boolean;
  /**
   * Log lines the app emitted for this request: OTel log records carrying its trace ID, forwarded by the in-guest bridge when tracing is enabled (runtime source; HTTP access lines are not repeated). Chronological; redacted and bounded. Empty without a trace ID or when the app exports no OTel logs.
   */
  logs: Array<DebugEvidenceLogLine>;
  /**
   * True when the request logged more than 20 lines; logs then holds its last 20.
   */
  logs_truncated: boolean;
  explanation: DebugEvidenceExplanation;
  generated_at: string;
};

