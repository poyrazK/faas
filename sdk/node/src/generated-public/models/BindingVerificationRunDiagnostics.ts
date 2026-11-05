/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { BindingVerificationBudget } from './BindingVerificationBudget.js';
import type { BindingVerificationDiagnosticBlocker } from './BindingVerificationDiagnosticBlocker.js';
import type { BindingVerificationDifference } from './BindingVerificationDifference.js';
import type { BindingVerificationRecoveryAction } from './BindingVerificationRecoveryAction.js';
/**
 * Public app receipt observations and recovery suggestions without admission authority.
 */
export type BindingVerificationRunDiagnostics = {
  budget?: BindingVerificationBudget;
  run_id: string;
  graph_run_id?: string;
  app: string;
  deployment_id: string;
  scope: string;
  status: 'pending' | 'passed' | 'blocked' | 'cancelled' | 'expired';
  checked_at: string;
  expires_at: string;
  /**
   * All applicable diagnostic reads succeeded; does not authorize admission.
   */
  complete: boolean;
  /**
   * Current public evidence satisfies current policy; does not confirm receipt completion.
   */
  check_passed?: boolean;
  blockers: Array<BindingVerificationDiagnosticBlocker>;
  changes: Array<BindingVerificationDifference>;
  recovery: Array<BindingVerificationRecoveryAction>;
};

