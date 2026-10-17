/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { BindingVerificationBudget } from './BindingVerificationBudget.js';
import type { BindingVerificationDiagnosticBlocker } from './BindingVerificationDiagnosticBlocker.js';
import type { BindingVerificationDifference } from './BindingVerificationDifference.js';
import type { BindingVerificationRecoveryAction } from './BindingVerificationRecoveryAction.js';
import type { BindingVerificationRunDiagnostics } from './BindingVerificationRunDiagnostics.js';
/**
 * Public graph receipt observations, safe differences and suggested recovery actions.
 */
export type ProjectBindingVerificationRunDiagnostics = {
  budget?: BindingVerificationBudget;
  run_id: string;
  project: string;
  environment: string;
  status: 'pending' | 'passed' | 'blocked' | 'cancelled' | 'expired';
  checked_at: string;
  expires_at: string;
  complete: boolean;
  members: Record<string, BindingVerificationRunDiagnostics>;
  blockers: Array<BindingVerificationDiagnosticBlocker>;
  changes: Array<BindingVerificationDifference>;
  recovery: Array<BindingVerificationRecoveryAction>;
};

