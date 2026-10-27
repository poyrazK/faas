/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Lets customers join the preview themselves. An opted-in account or organization on an eligible plan gets the flag when no rule matched. Disabling it ignores existing opt-ins without deleting them.
 */
export type PlatformFlagSelfServe = {
  enabled: boolean;
  /**
   * Eligible plans; empty means every plan. A flag that releases a paid capability must list only entitled plans.
   */
  plans?: Array<'free' | 'hobby' | 'pro' | 'scale'>;
  /**
   * Cap on opted-in accounts plus organizations; 0 means 5000.
   */
  max_subjects?: number;
};

