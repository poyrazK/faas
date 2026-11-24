/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Retained acknowledgement metadata independent of readiness state.
 */
export type MCPReadinessAcknowledgement = {
  /**
   * Provider provenance for this MCPReadinessAcknowledgement; absence denotes a local account action.
   */
  source?: 'pagerduty';
  /**
   * PagerDuty event associated with this MCPReadinessAcknowledgement.
   */
  provider_event_id?: string;
  /**
   * Provider incident associated with this MCPReadinessAcknowledgement.
   */
  provider_incident_id?: string;
  /**
   * Signed provider event time retained in this MCPReadinessAcknowledgement.
   */
  provider_occurred_at?: string;
  request_id: string;
  created_by: string;
  created_at: string;
  reason: string;
  /**
   * Omitted for acknowledgement until release or incident resolution.
   */
  expires_at?: string;
};

