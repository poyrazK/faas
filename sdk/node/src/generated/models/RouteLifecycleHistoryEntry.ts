/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteGateDecision } from './RouteGateDecision.js';
import type { RouteLifecycleHistoryApproval } from './RouteLifecycleHistoryApproval.js';
import type { RouteLifecycleHistoryCapture } from './RouteLifecycleHistoryCapture.js';
/**
 * Retained production review outcome and bounded historical evidence.
 */
export type RouteLifecycleHistoryEntry = {
  id: string;
  app_id: string;
  deployment_id: string;
  reviewed_at: string;
  scope: string;
  outcome: 'applied' | 'blocked';
  recovery: boolean;
  decision: RouteGateDecision;
  evidence_available: boolean;
  truncated: boolean;
  captures: Array<RouteLifecycleHistoryCapture>;
  graph_ids: Array<string>;
  approvals: Array<RouteLifecycleHistoryApproval>;
};

