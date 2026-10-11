/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowSpec } from './WorkflowSpec.js';
/**
 * Saved draft, publication, ownership and opaque revision of one automation.
 */
export type AutomationResponse = {
  /**
   * Runtime failure guard blocks automatic admissions independently of configured enabled intent.
   */
  failure_paused?: boolean;
  name: string;
  /**
   * Opaque monotonically increasing revision. Zero creates the first draft. A stale value returns automation_version_conflict.
   */
  version: number;
  source: 'manifest' | 'dashboard' | 'draft';
  draft: WorkflowSpec;
  published?: WorkflowSpec;
  published_version?: number;
  enabled: boolean;
  updated_at?: string;
};

