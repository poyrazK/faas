/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Returned by POST /v1/billing/cancel (issue #242). The CLI
 * renders effective_at as "your apps will stop on <date>".
 * cancel_scheduled is always true on 200; the 409 path returns
 * a Problem with a friendly "already cancelled" hint so a
 * re-cancel idempotency click does not surface as a server error.
 *
 */
export type BillingCancelResponse = {
  /**
   * Always true; the absence of an active subscription returns 409 instead.
   */
  cancel_scheduled: boolean;
  /**
   * RFC 3339 instant at which the subscription terminates (Stripe: current_period_end; Paddle: next month-rollover).
   */
  effective_at: string;
};

