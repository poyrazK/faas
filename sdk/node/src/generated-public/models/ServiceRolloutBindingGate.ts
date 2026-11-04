/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { BindingCheckFinding } from './BindingCheckFinding.js';
/**
 * Bounded progress of an exact binding check at the service routing boundary. Passed confirms the routing transaction; scheduler ACK and drain completion are reported by the enclosing handoff phase.
 */
export type ServiceRolloutBindingGate = {
  request_id: string;
  action: 'promote' | 'abort';
  deployment_id: string;
  status: 'pending' | 'blocked' | 'passed';
  code?: string;
  blockers?: Array<BindingCheckFinding>;
  checked_at?: string | null;
  policy_revision?: number;
  audit_id?: string;
};

