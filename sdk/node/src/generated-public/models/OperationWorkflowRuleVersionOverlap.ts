/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationSubject } from './OperationSubject.js';
import type { OperationWorkflowDecisionOutcomeEvidence } from './OperationWorkflowDecisionOutcomeEvidence.js';
/**
 * Bounded observed multi-version example in stable workflow identity order. Up to 16 earliest-per-version decision reports appear in revision order; ties do not imply business ordering. Missing history may hide other versions or earlier terminal reports. Evidence links explain observed membership and do not establish that a policy was replaced.
 */
export type OperationWorkflowRuleVersionOverlap = {
  platform_tenant_id?: string;
  subject: OperationSubject;
  instance_id: string;
  operation_id: string;
  history_complete: boolean;
  eligible: boolean;
  evidence_truncated: boolean;
  evidence: Array<OperationWorkflowDecisionOutcomeEvidence>;
};

