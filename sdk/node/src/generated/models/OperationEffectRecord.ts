/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Immutable effect identity and current delivery status, or platform-captured intent for a rehearsal. Captured effects have no outbound delivery.
 */
export type OperationEffectRecord = {
  id: string;
  name: string;
  generation: number;
  webhook_id?: string;
  /**
   * Stable across webhook retries; equals the effect ID.
   */
  delivery_id?: string;
  /**
   * Present only for platform-captured rehearsal effects; no outbound delivery was queued.
   */
  rehearsal_run_id?: string;
  type?: string;
  /**
   * recorded is an opaque effect with no adapter; captured is retained rehearsal intent with no outbound delivery; unavailable indicates deleted or pruned delivery history. Completion of the operation does not imply delivery succeeded.
   */
  status: 'recorded' | 'captured' | 'pending' | 'in_flight' | 'succeeded' | 'failed' | 'dead' | 'unavailable';
  attempt: number;
  last_error?: string;
};

