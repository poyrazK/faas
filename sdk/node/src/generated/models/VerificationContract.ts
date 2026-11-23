/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * An app's stored gregale.verify.yaml contract and its revision.
 */
export type VerificationContract = {
  app_id: string;
  /**
   * The stored gregale.verify.yaml contract as JSON (version, checks).
   */
  contract: Record<string, any>;
  /**
   * SHA-256 of the canonical contract encoding.
   */
  contract_sha256: string;
  check_count: number;
  revision: number;
  updated_at: string;
};

