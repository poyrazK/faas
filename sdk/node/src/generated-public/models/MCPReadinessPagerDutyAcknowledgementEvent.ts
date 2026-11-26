/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Immutable minimal receipt committed atomically with any provider acknowledgement and reminder suppression.
 */
export type MCPReadinessPagerDutyAcknowledgementEvent = {
  /**
   * Provider decision; absent on legacy acknowledgement receipts. Release event receipts are independent per destination.
   */
  action?: 'acknowledge' | 'release';
  /**
   * Authenticated pinned receiver; absent on legacy primary receipts.
   */
  destination?: 'primary' | 'fallback';
  /**
   * Authenticated receiver region retained in this provider event receipt.
   */
  region: 'us' | 'eu';
  /**
   * Authenticated PagerDuty service retained in this provider event receipt.
   */
  service_id: string;
  id: string;
  event_id: string;
  incident_id: string;
  provider_incident_id: string;
  occurred_at: string;
  received_at: string;
  policy_version: number;
  outcome: 'acknowledged' | 'already_acknowledged' | 'stale' | 'resolved' | 'released' | 'retained' | 'not_acknowledged';
  attention_version: number;
};

