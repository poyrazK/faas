/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Redacted evidence for one request or webhook step in a scenario.
 */
export type APISandboxRunStep = {
  index: number;
  title: string;
  method: string;
  /**
   * Saved request path without its query string.
   */
  path?: string;
  partner_request?: boolean;
  /**
   * Gregale intentionally ignored the first successful HTTP response.
   */
  acknowledgement_dropped?: boolean;
  /**
   * Business effects reported by the partner.
   */
  partner_applied_count?: number;
  kind?: 'stripe_webhook' | 'webhook_duplicate' | 'webhook_processing' | 'outbound_job' | 'outbound_webhook_queued' | 'outbound_webhook_attempt' | 'outbound_webhook_payload' | 'partner_webhook_outcome';
  event_type?: string;
  invocation_id?: string;
  invocation_state?: 'pending' | 'dispatching' | 'completed' | 'failed' | 'cancelled' | 'superseded' | 'expired' | 'dead_letter';
  delivery_attempts?: number;
  delivery_id?: string;
  attempt_number?: number;
  observed_at?: string;
  expected_status?: number;
  http_status?: number;
  duration_ms: number;
  request_id?: string;
  failure_code?: string;
  failed_assertion?: number;
  failed_pointer?: string;
};

