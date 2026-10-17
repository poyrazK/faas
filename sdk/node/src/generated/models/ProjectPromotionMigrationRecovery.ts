/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectPromotionMigrationRecoveryRequest } from './ProjectPromotionMigrationRecoveryRequest.js';
/**
 * Durable acknowledgement and deadline for an operator's promotion migration recovery decision.
 */
export type ProjectPromotionMigrationRecovery = {
  request: ProjectPromotionMigrationRecoveryRequest;
  status: 'quiescing' | 'verifying' | 'resumed' | 'resolved' | 'blocked';
  reason?: string;
  operator_account_id: string;
  requested_at: string;
  deadline: string;
  resolved_at?: string;
};

