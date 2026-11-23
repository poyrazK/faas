/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Minimal ownership of an active provider acknowledgement; no credentials or integration fingerprints are exposed.
 */
export type MCPReadinessPagerDutyAcknowledgementHold = {
  destination: 'primary' | 'fallback';
  provider_incident_id: string;
  provider_event_id: string;
  service_id: string;
  region: 'us' | 'eu';
  occurred_at: string;
  received_at: string;
  policy_version: number;
  /**
   * False requires local release; includes acknowledgement-only bindings and ambiguous ownership.
   */
  release_sync_enabled: boolean;
};

