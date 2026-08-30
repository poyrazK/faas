/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One alert_deliveries row as surfaced by
 * GET /v1/apps/{slug}/alerts/{id}/deliveries (ADR-123 PR-D).
 * Test rows (IsTest=true) are written by Dispatcher.DispatchTest
 * (the customer-facing "send test alert" path); the production
 * read (include_test=false) hides them, the operator read
 * (?include_test=true) surfaces them.
 *
 */
export type AlertDeliveryResponse = {
  id: string;
  rule_id: string;
  account_id: string;
  /**
   * omitted on account-wide rules
   */
  app_id?: string;
  /**
   * rule_id + ':' + cooldown bucket (production) or delivery_id + ':test' (test path)
   */
  idempotency_key: string;
  status: 'pending' | 'delivered' | 'failed';
  attempt_count: number;
  /**
   * 0 when the attempt never reached the wire
   */
  last_status_code?: number;
  /**
   * truncated server-side via dashboard.FormatAlertError (log-injection-safe)
   */
  last_error?: string;
  observed_value: number;
  fired_at: string;
  /**
   * omitted until status=delivered
   */
  delivered_at?: string;
  /**
   * true iff the row was written by Dispatcher.DispatchTest
   */
  is_test: boolean;
};

