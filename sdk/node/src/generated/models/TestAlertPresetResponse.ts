/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Response body for POST /v1/apps/{slug}/alert-presets/{name}/test
 * (and the sibling dashboard form-POST handler). The handler
 * dispatches a synthetic event to the customer's configured
 * webhook URL with `payload.test == true`; this shape confirms
 * the dispatch completed and surfaces the delivery_id the
 * customer's webhook receiver should echo back.
 * (issue #1233 / ADR-123 PR-C commit 2)
 *
 */
export type TestAlertPresetResponse = {
  /**
   * Always "sent" on 200. A dispatch failure returns 502
   * with an RFC 7807 problem document (NOT this shape).
   *
   */
  status: 'sent';
  /**
   * Always true on 200. Discriminator customers can key off
   * in their webhook receiver to skip production alerting
   * paths (e.g. PagerDuty incidents) for test dispatches.
   *
   */
  test: boolean;
  /**
   * 32-char lowercase hex identifier for this dispatch
   * (16 random bytes from crypto/rand encoded as hex). The
   * audit log row (`alert_preset.test_sent`) carries the
   * same delivery_id so the customer can correlate by
   * timestamp + delivery_id without leaking via UUID dashes.
   *
   */
  delivery_id: string;
  /**
   * Number of dispatch attempts the webhookout.Dispatcher
   * made before reaching the final state (1..MaxAttempts).
   * Customers can use this to tune their receiver's SLA
   * (e.g. a successful first attempt vs. a successful
   * retry after a transient 502 from the receiver).
   *
   */
  attempts: number;
};

