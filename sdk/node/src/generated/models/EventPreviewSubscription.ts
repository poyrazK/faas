/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A bounded sample of an enabled subscription considered by the event router.
 */
export type EventPreviewSubscription = {
  app_slug: string;
  subscription_id: string;
  source: string;
  type: string;
  /**
   * Normalized content filter declared by this subscription.
   */
  filter: Record<string, any>;
  /**
   * would_deliver, content_filter_mismatch, pattern_mismatch, tenant_mismatch, or an invalid_subscription explanation.
   */
  reason: string;
};

