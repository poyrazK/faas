/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationBusinessConditionResult } from './OperationBusinessConditionResult.js';
/**
 * Application-reported choice of one named path in a compound prerequisite group of the selected pinned transition. Each group may appear once. Choices do not authorize or execute actions.
 */
export type OperationWorkflowPathSelection = {
  /**
   * Optional application condition snapshot; requires a decision fact reference and exact equality with that fact. Omitted for legacy decisions.
   */
  decision_conditions?: Array<OperationBusinessConditionResult>;
  group_id: string;
  path_id: string;
  /**
   * Optional business decision milestone in this report evidence list and application transaction. When present all decision fields are required and must match its payload.
   */
  decision_milestone_id?: string;
  decision_code?: string;
  decision_description?: string;
  decision_rule_id?: string;
  decision_rule_version?: string;
};

