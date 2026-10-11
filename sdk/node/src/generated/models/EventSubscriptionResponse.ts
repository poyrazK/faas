/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventRoutingRetryPolicy } from './EventRoutingRetryPolicy.js';
/**
 * One manifest-declared event subscription reconciled for an app.
 */
export type EventSubscriptionResponse = {
  /**
   * Schema versions in the event subscription response: exact case-sensitive schema versions. Empty or omitted accepts all versions; a nonempty selection excludes unversioned events. Selection is captured at publication or backfill creation.
   */
  schema_versions?: Array<string>;
  routing_retry_policy?: EventRoutingRetryPolicy;
  id: string;
  app_id: string;
  /**
   * Event source pattern, including supported wildcard forms.
   */
  source: string;
  /**
   * Event type pattern, including supported wildcard forms.
   */
  type: string;
  /**
   * Normalized JSON filter evaluated by the event matcher.
   */
  filter: Record<string, any>;
  /**
   * Named app policy for keyed event deliveries, when configured.
   */
  work_policy?: string;
  /**
   * Dot selector into the CloudEvents envelope for the work key.
   */
  work_key?: string;
  /**
   * Optional dot selector for an application fairness group; defaults to work_key.
   */
  work_fairness_key?: string;
  /**
   * Action taken on a matching event.
   */
  work_action?: 'invoke' | 'cancel_pending';
  /**
   * Whether this subscription waits for earlier matching keyed deliveries before routing.
   */
  ordered?: boolean;
  enabled: boolean;
  created_at: string;
  updated_at: string;
};

