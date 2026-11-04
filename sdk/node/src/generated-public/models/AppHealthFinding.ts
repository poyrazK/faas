/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A safe, independent readiness finding for a serving replica.
 */
export type AppHealthFinding = {
  reason: 'readiness_configuration_unavailable' | 'readiness_missing' | 'readiness_timestamp_invalid' | 'required_probe_unready' | 'node_evidence_missing' | 'node_unavailable' | 'node_timestamp_invalid' | 'node_heartbeat_stale' | 'node_recovering';
  status: 'fail' | 'unknown';
  /**
   * Safe explanation without probe output or infrastructure addresses.
   */
  detail: string;
  deployment_id: string;
  instance_id: string;
  /**
   * node, configuration, primary_app, or sidecar followed by a colon and the declared companion name.
   */
  source: string;
  /**
   * Recorded readiness transition or last node heartbeat; absent for missing evidence. A transition time is not a probe heartbeat.
   */
  observed_at?: string;
};

