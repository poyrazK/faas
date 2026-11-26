/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * The exact unpriced model usage selected for administrative approval on an app consumer draft.
 */
export type APIConsumerUsageWaiverRequest = {
  /**
   * Latest held statement revision reviewed for this waiver.
   */
  source_statement_id: string;
  /**
   * App meter being waived in app:<meter> form.
   */
  unit: string;
  /**
   * Reported alias without an effective price in the selected window.
   */
  model: string;
  /**
   * Inclusive UTC-minute start within the source statement period.
   */
  since: string;
  /**
   * Exclusive UTC-minute end within the source period. Must be a closed boundary.
   */
  until: string;
  /**
   * Exact count of remaining unpriced units for the model and window on the source draft.
   */
  units: number;
  /**
   * Reason for approving zero charge. The API limit is 500 UTF-8 bytes.
   */
  reason: string;
};

