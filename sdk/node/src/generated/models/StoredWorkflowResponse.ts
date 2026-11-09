/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { StoredWorkflowTestReport } from './StoredWorkflowTestReport.js';
import type { StoredWorkflowTestSuite } from './StoredWorkflowTestSuite.js';
import type { WorkflowSpec } from './WorkflowSpec.js';
/**
 * Saved draft, publication, ownership and opaque revision of one stored workflow.
 */
export type StoredWorkflowResponse = {
  test_suite?: StoredWorkflowTestSuite;
  test_report?: StoredWorkflowTestReport;
  /**
   * Runtime failure guard blocks automatic admissions independently of configured enabled intent.
   */
  failure_paused?: boolean;
  name: string;
  /**
   * Opaque monotonically increasing revision. Zero creates the first draft. A stale value returns stored_workflow_version_conflict.
   */
  version: number;
  source: 'manifest' | 'dashboard' | 'draft';
  draft: WorkflowSpec;
  published?: WorkflowSpec;
  published_version?: number;
  enabled: boolean;
  updated_at?: string;
};

