/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Safe persisted progress for one event receiver without ownership tokens or credentials.
 */
export type MCPReadinessDestinationDelivery = {
  /**
   * Transport for this receiver delivery; omission denotes a legacy webhook.
   */
  provider?: 'webhook' | 'pagerduty';
  /**
   * PagerDuty transport progress; accepted means a validated 202 acknowledgement rather than a confirmed page.
   */
  provider_status?: 'pending' | 'delivering' | 'retrying' | 'accepted' | 'suppressed' | 'abandoned' | 'superseded';
  destination: 'primary' | 'fallback';
  status: 'pending' | 'failed' | 'delivering' | 'delivered' | 'suppressed' | 'abandoned' | 'superseded';
  attempt_count: number;
  last_status_code?: number;
  last_delivery_id?: string;
  next_attempt_at?: string;
  lease_until?: string;
};

