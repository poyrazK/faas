/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Immutable recovery lifecycle or control action with actor identity and previous settings.
 */
export type EventRecoveryHistoryEntry = {
  id: number;
  occurred_at: string;
  action: 'created' | 'paused' | 'resumed' | 'rate_changed' | 'cancelled' | 'expired';
  actor_kind: 'account' | 'api_key' | 'internal' | 'system';
  /**
   * Authenticated account or API key ID, or an internal/system identifier. No credential values.
   */
  actor_id: string;
  reason?: string;
  previous_state: '' | 'running' | 'paused' | 'completed' | 'cancelled';
  state: 'running' | 'paused' | 'completed' | 'cancelled';
  previous_rate: number;
  rate: number;
};

