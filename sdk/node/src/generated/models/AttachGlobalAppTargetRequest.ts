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
   * Regional admin API key used only for verification. Shared apps require a current organization owner/admin key.
   */
  regional_api_key: string;
};

