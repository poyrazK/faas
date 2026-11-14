/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ToolGrantAccess } from './ToolGrantAccess.js';
/**
 * Single-tool catalog projection excluding sibling tools, prompts, resources, test names and credentials. Projected and full publication digests describe different bytes.
 */
export type ToolRegistryTool = {
  /**
   * Active grants for the exact requested tool and current key; never a dispatch receipt
   */
  grants?: Array<ToolGrantAccess>;
  version: string;
  account_id: string;
  app: string;
  app_id: string;
  name: string;
  deployment_id: string;
  source_sha256: string;
  deployment_source_sha256: string;
  /**
   * Original runtime catalog pin
   */
  catalog_sha256: string;
  /**
   * Digest of the returned single-tool projection
   */
  catalog_contract_sha256: string;
  /**
   * Digest of the complete archived publication contract
   */
  published_catalog_contract_sha256: string;
  catalog_base64: string;
  access_mode: 'open' | 'external-oauth';
  required_scopes: Array<string>;
  evidence_origin: 'publisher_attested';
  availability: 'not_checked';
};

