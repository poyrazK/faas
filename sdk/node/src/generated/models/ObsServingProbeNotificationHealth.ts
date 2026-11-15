/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsServingProbeNotification } from './ObsServingProbeNotification.js';
import type { ObsServingProbeNotificationCounts } from './ObsServingProbeNotificationCounts.js';
/**
 * Optional, independent evidence for direct compute-node webhook and
 * heartbeat submissions. Counts cover every expected node/channel; the
 * bounded preview orders failures first, then unknowns, successes and
 * disabled channels. Available describes evidence completeness and may
 * include failed submissions. Historical success does not establish
 * current reachability, provider processing or human receipt. This
 * section does not change serving or deployment qualification results.
 *
 */
export type ObsServingProbeNotificationHealth = {
  state: 'available' | 'partial' | 'unknown' | 'not_applicable';
  reason?: 'inventory_unavailable' | 'inventory_not_configured' | 'prometheus_not_configured' | 'zero_expected_probes' | 'incomplete_evidence' | 'metadata_inconsistent';
  expected_channels: number | null;
  counts: (ObsServingProbeNotificationCounts | null);
  limit: number;
  omitted: number;
  truncated: boolean;
  channels: Array<ObsServingProbeNotification>;
};

