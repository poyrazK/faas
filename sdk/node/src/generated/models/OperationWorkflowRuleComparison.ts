/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowRuleVersionStatistics } from './OperationWorkflowRuleVersionStatistics.js';
/**
 * One pinned contract, decision code and rule ID with side-by-side version columns. Compared status means at least two returned versions have eligible observations; it is not a significance test or a claim of improvement. At most 16 canonical identities and 16 lexical versions per identity are retained.
 */
export type OperationWorkflowRuleComparison = {
  contract_version: number;
  decision_code: string;
  rule_id: string;
  status: 'compared' | 'insufficient_data';
  versions_truncated: boolean;
  versions: Array<OperationWorkflowRuleVersionStatistics>;
};

