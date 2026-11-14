/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ToolPublicationCheck } from './ToolPublicationCheck.js';
/**
 * Immutable publisher-attested evidence bounded to 512 KiB. Completed outcomes require the complete observed catalog and exact passing tests. Failed or uncertain outcomes contain only status and protocol_verified=false.
 */
export type ToolPublicationOutcome = {
  status: 'completed' | 'failed' | 'outcome_unknown';
  deployment_id?: string;
  /**
   * Runtime integrity pin; separate from the caller-visible contract digest
   */
  catalog_sha256?: string;
  /**
   * Canonical format-2 MCP contract bytes, at most 256 KiB decoded; base64 preserves byte identity through JSONB
   */
  catalog_base64?: string;
  protocol_verified: boolean;
  checks?: Array<ToolPublicationCheck>;
};

