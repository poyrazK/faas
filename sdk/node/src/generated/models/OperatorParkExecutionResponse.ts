/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Verified force-park execution evidence for the original VM generation.
 */
export type OperatorParkExecutionResponse = {
  phase: 'prepared' | 'started' | 'acknowledged' | 'outcome_unknown' | 'completed' | 'failed' | 'cancelled';
  node_id: string;
  wake_id: string;
  generation: string;
  action: 'capture' | 'stop';
  /**
   * Capture and original VM cleanup were durably acknowledged.
   */
  capture_completed: boolean;
  stopped_at?: string;
  terminal_state?: 'parked' | 'stopped';
  /**
   * The verified result is stopped and requires a cold boot.
   */
  cold_boot_required: boolean;
  reason?: string;
  updated_at: string;
};

