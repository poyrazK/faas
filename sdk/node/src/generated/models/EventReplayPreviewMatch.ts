/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Matching retained-event metadata and original recipient membership.
 */
export type EventReplayPreviewMatch = {
  /**
   * Age limit of the current subscription is exceeded at observation time; historical backfill requires an explicit override to admit it.
   */
  delivery_expired: boolean;
  delivery_deadline_at?: string;
  event_id: string;
  event_source: string;
  event_type: string;
  schema_version?: string;
  accepted_at: string;
  /**
   * Membership of the immutable original recipient snapshot; unknown indicates a legacy receipt without a snapshot. This does not indicate handler completion or replay eligibility.
   */
  original_recipient: 'captured' | 'not_captured' | 'unknown';
  receipt_url: string;
};

