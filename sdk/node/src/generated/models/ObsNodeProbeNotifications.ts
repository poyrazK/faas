/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsServingProbeInventory } from './ObsServingProbeInventory.js';
import type { ObsServingProbeNotification } from './ObsServingProbeNotification.js';
/**
 * Complete direct submission evidence for a selected node, independent of
 * the fleet preview. Expected is null when inventory cannot establish
 * intent, false when fresh configured intent excludes the node, and true
 * when both channel slots are expected. Available describes evidence
 * completeness, not current provider health or external receipt.
 *
 */
export type ObsNodeProbeNotifications = {
  generated_at: string;
  scope: string;
  node: string;
  state: 'available' | 'partial' | 'unknown' | 'not_applicable';
  reason?: 'prometheus_not_configured' | 'inventory_unavailable' | 'inventory_not_configured' | 'node_not_expected' | 'incomplete_evidence';
  max_age_seconds: number;
  inventory: ObsServingProbeInventory;
  expected: boolean | null;
  max_result_age_seconds: number | null;
  channels: Array<ObsServingProbeNotification>;
};

