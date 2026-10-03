/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One-request proof of regional ownership. The key is never retained or returned. Body is bounded to 4096 bytes.
 */
export type AttachGlobalAppTargetRequest = {
  regional_app_slug: string;
  /**
   * Existing regional personal account-admin API key used only for this verification.
   */
  regional_api_key: string;
};

