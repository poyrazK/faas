/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPContractChange } from './MCPContractChange.js';
/**
 * One production candidate's comparison with the tool contract.
 */
export type MCPContractCheck = {
  deployment_id: string;
  verdict: 'unchanged' | 'compatible' | 'review' | 'breaking' | 'unverified';
  baseline_sha256: string;
  /**
   * Absent when the catalog could not be listed (unverified).
   */
  contract_sha256?: string;
  overridden: boolean;
  changes: Array<MCPContractChange>;
  checked_at: string;
  /**
   * When the candidate went live.
   */
  accepted_at?: string;
};

