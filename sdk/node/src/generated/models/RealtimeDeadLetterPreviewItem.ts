/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Realtime Dead Letter Preview Item.
 */
export type RealtimeDeadLetterPreviewItem = {
  sequence: string;
  dead_since: string;
  attempts: number;
  failure_code: string;
  /**
   * A current unexpired same-key lease blocks replay; observed only.
   */
  ordering_blocked: boolean;
  /**
   * Nominal retention expiry; warning grace may postpone actual cleanup.
   */
  expires_at: string | null;
};

