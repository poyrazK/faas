/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowDiagnosticBlocker } from './WorkflowDiagnosticBlocker.js';
/**
 * A continuation plan based on the current run generation and recovery admission checks.
 */
export type WorkflowResumePreview = {
  /**
   * Advisory eligibility at observation. Actual resume rechecks state and quotas.
   */
  eligible: boolean;
  /**
   * Observed generation for a subsequent resume request; stale generations are rejected.
   */
  expected_resume_count: number;
  /**
   * Structurally eligible steps that would reopen even if a temporary admission blocker prevents continuation. Empty when recovery planning is unsafe.
   */
  reopened_steps: Array<string>;
  /**
   * Persisted steps whose current state is retained including completed actions and untaken branches. Sorted by name as are reopened_steps and diagnostic steps.
   */
  preserved_steps: Array<string>;
  /**
   * First deterministic planner blocker plus independent admission blockers. Empty only when eligible. Codes omit private values.
   */
  blockers: Array<WorkflowDiagnosticBlocker>;
};

