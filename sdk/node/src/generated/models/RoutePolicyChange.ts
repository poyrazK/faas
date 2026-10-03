/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CreateEdgeRuleRequest } from './CreateEdgeRuleRequest.js';
import type { RoutePolicyImpact } from './RoutePolicyImpact.js';
import type { UpdateEdgeRuleRequest } from './UpdateEdgeRuleRequest.js';
/**
 * Generated concrete or family selector creation or action-only update with expected outcome and affected traffic.
 */
export type RoutePolicyChange = {
  operation: string;
  kind: string;
  /**
   * Declared group used for a consolidated budget selector.
   */
  budget_group?: string;
  rule_id?: string;
  simulated_rule_id?: string;
  create?: CreateEdgeRuleRequest;
  update?: UpdateEdgeRuleRequest;
  expected: string;
  before?: string;
  after: string;
  displaced_rule_ids?: Array<string>;
  impact: RoutePolicyImpact;
  reason: string;
};

