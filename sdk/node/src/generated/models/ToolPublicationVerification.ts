/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ToolPublicationVerificationOutcome } from './ToolPublicationVerificationOutcome.js';
/**
 * Durable public candidate-test metadata; never includes a token, secret reference or ciphertext.
 */
export type ToolPublicationVerification = {
  request_sha256: string;
  publication_id: string;
  key_id: string;
  app_id: string;
  deployment_id: string;
  deployment_source_sha256: string;
  catalog_sha256: string;
  /**
   * Candidate readiness configuration and credential authority fingerprint
   */
  readiness_artifact_sha256?: string;
  status: 'pending' | 'approved' | 'running' | 'completed' | 'failed' | 'revoked' | 'not_started' | 'outcome_unknown';
  created_at: string;
  deadline: string;
  outcome?: ToolPublicationVerificationOutcome;
};

