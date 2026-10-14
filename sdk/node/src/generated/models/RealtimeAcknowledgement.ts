/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Realtime Acknowledgement.
 */
export type RealtimeAcknowledgement = {
  checkpoint_sequence: string;
  /**
   * Latest advancing ACK; null after initialization or reset
   */
  acknowledged_sequence: string | null;
  acknowledged_at: string | null;
  updated_at: string;
};

