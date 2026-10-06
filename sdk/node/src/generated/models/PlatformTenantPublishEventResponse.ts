/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Durable acceptance receipt for a tenant-scoped published event.
 */
export type PlatformTenantPublishEventResponse = {
  /**
   * Canonical event id scoped by tenant
   */
  id: string;
  /**
   * Caller-chosen event identifier.
   */
  client_event_id: string;
  accepted_at: string;
  /**
   * Tenant-authenticated relative URL for this event receipt.
   */
  receipt_url: string;
};

