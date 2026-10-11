/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AlertRollbackDeploymentEvidence } from './AlertRollbackDeploymentEvidence.js';
import type { BindingCheckFinding } from './BindingCheckFinding.js';
/**
 * Durable exact canary, active service, or opt-in completed-release rollback captured atomically with one production alert fire. Historical actions link one checked rollback operation and complete only after readiness, checked routing and service handoff barriers. Service actions pin the retained predecessor and complete only after the matching abort handoff finishes. Binding evidence is never reusable. Unavailable or ambiguous selections fail closed. Deleting the alert rule deletes its delivery and action ledger.
 */
export type AlertRollback = {
  deployment_evidence?: AlertRollbackDeploymentEvidence;
  historical?: boolean;
  rollback_operation_id?: string;
  rollback_phase?: 'preparing' | 'ready' | 'blocked' | 'routing' | 'complete' | 'failed';
  rollback_routing_audit_id?: string;
  id: string;
  rule_id: string;
  account_id: string;
  app_id: string;
  scope: string;
  candidate_deployment_id?: string;
  predecessor_deployment_id?: string;
  status: 'pending' | 'blocked' | 'complete' | 'failed';
  reason: string;
  code?: string;
  blockers?: Array<BindingCheckFinding>;
  observed_value: number;
  fired_at: string;
  updated_at: string;
  completed_at?: string | null;
  audit_id?: string;
  service?: boolean;
  service_request_id?: string;
  service_phase?: 'pending' | 'routing' | 'draining' | 'complete';
  service_routing_audit_id?: string;
};

