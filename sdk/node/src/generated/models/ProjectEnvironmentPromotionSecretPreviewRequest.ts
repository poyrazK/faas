/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Environment promotion preview inputs referencing staged immutable secrets without plaintext values.
 */
export type ProjectEnvironmentPromotionSecretPreviewRequest = {
  from_environment: string;
  sync_config: boolean;
  verify: boolean;
  /**
   * Complete destination secret key/version maps by workload. Values must be staged immutable UUIDs, never plaintext.
   */
  secrets: Record<string, Record<string, string>>;
};

