/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Immutable effect identity and current delivery status from the signed webhook ledger.
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
  type?: string;
  /**
   * recorded is an opaque effect with no adapter; unavailable indicates deleted or pruned delivery history. Completion of the operation does not imply delivery succeeded.
   */
  status: 'recorded' | 'pending' | 'in_flight' | 'succeeded' | 'failed' | 'dead' | 'unavailable';
  attempt: number;
  last_error?: string;
};

