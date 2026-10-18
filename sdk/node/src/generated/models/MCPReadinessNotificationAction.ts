/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Version-fenced decision against a receiver head or the oldest event using an app-scoped idempotency key.
 */
export type MCPReadinessNotificationAction = {
  /**
   * Selected receiver queue; omitted for an event-wide decision.
   */
  destination?: 'primary' | 'fallback';
  /**
   * Reuse this UUID and the identical request body when retrying the same decision.
   */
  request_id: string;
  event_id: string;
  action: 'retry' | 'abandon';
  /**
   * Current head delivery_version from queue inspection.
   */
  expected_version: number;
  /**
   * Nonblank operator explanation limited to 500 UTF-8 bytes.
   */
  reason: string;
};

