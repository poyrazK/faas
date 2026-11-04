/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowInitialFailure } from './WorkflowInitialFailure.js';
import type { WorkflowRunRecoveryTarget } from './WorkflowRunRecoveryTarget.js';
/**
 * Read-only initial-failure decision for a proposed next policy version. Eligibility reports policy filters and fresh safety diagnostics. Preparation reports duplicate, review-retention, cooldown or action-budget pressure. Execution reports whether that prepared candidate would be submitted automatically, including durable execution budget, cooldown, unresolved app recovery and native account quota. Target is advisory and omitted for excluded or unsafe failures; it grants no execution authority.
 */
export type WorkflowRecoveryInitialPolicyChangeCandidate = {
  failure: WorkflowInitialFailure;
  eligibility: string;
  preparation: string;
  execution: string;
  blockers: Array<string>;
  target?: WorkflowRunRecoveryTarget;
};

