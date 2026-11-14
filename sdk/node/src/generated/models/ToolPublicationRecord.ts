/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ToolPublicationIntent } from './ToolPublicationIntent.js';
import type { ToolPublicationOutcome } from './ToolPublicationOutcome.js';
/**
 * Account-owned durable publication review and immutable outcome bound to native deployment source identity.
 */
export type ToolPublicationRecord = {
  id: string;
  account_id: string;
  intent: ToolPublicationIntent;
  status: 'running' | 'completed' | 'failed' | 'outcome_unknown';
  outcome?: ToolPublicationOutcome;
  /**
   * Native source-archive digest, distinct from the reviewed source-tree digest
   */
  deployment_source_sha256?: string;
  created_at: string;
  deadline: string;
  finished_at?: string;
  evidence_origin: 'publisher_attested';
};

