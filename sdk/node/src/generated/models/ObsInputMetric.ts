/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsInputMetricCounts } from './ObsInputMetricCounts.js';
/**
 * Presence and freshness of one idle-safe readiness gauge on each fresh successful scrape target.
 */
export type ObsInputMetric = {
  metric: 'apid_daemon_ready' | 'gatewayd_public_daemon_ready' | 'gatewayd_daemon_ready' | 'schedd_daemon_ready' | 'vmmd_daemon_ready';
  state: 'healthy' | 'degraded' | 'unknown' | 'not_applicable';
  reason?: 'target_evidence_unavailable' | 'no_fresh_up_targets' | 'zero_expected_targets' | 'source_unavailable' | 'invalid_evidence' | 'metric_sample_gap';
  /**
   * Gauge sample partition over all fresh up targets; null if this metric assessment is unknown.
   */
  counts: (ObsInputMetricCounts | null);
};

