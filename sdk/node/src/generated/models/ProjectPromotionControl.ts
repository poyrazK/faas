/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectPromotionControlRequest } from './ProjectPromotionControlRequest.js';
/**
 * Durable promotion abort or restoration decision, including its operator, deadline and resolution.
 */
export type ProjectPromotionControl = {
  request: ProjectPromotionControlRequest;
  action: 'abort' | 'recover';
  status: 'pending' | 'aborted' | 'restored' | 'blocked';
  operator_account_id: string;
  requested_at: string;
  deadline: string;
  resolved_at?: string;
  reason?: string;
};

