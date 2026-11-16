/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Completed publisher-attested tool version and access requirements; current availability is not inferred from history.
 */
export type ToolRegistryVersion = {
  /**
   * Immutable app identity when available
   */
  app_id?: string;
  version: string;
  account_id: string;
  app: string;
  tools: Array<string>;
  deployment_id: string;
  source_sha256: string;
  deployment_source_sha256: string;
  catalog_sha256: string;
  evidence_origin: 'publisher_attested';
  access_mode: 'open' | 'external-oauth';
  availability: 'not_checked';
};

