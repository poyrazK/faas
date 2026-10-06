/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Compare-and-set binding release enforcement for one deployment scope, including evidence age, application acknowledgements and an explicit change reason.
 */
export type SetBindingReleasePolicyRequest = {
  mode: 'off' | 'enforce';
  expected_revision: number;
  /**
   * Whole-second Go duration between 1s and 24h.
   */
  max_verification_age?: string;
  require_application_ack?: boolean;
  /**
   * Required for off mode; single line, at most 256 UTF-8 bytes.
   */
  reason?: string;
};

