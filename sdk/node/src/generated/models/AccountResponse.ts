/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AccountLimits } from './AccountLimits.js';
/**
 * Account profile: id, email, plan, status, limits snapshot, current-month usage, and total app count.
 */
export type AccountResponse = {
  id: string;
  email: string;
  plan: 'free' | 'hobby' | 'pro' | 'scale';
  status: 'active' | 'past_due' | 'suspended' | 'deleted_pending';
  limits: AccountLimits;
  usage_gb_hours: number;
  app_count: number;
  github_install_id?: string | null;
  plan_change_status?: string;
  requested_plan?: 'free' | 'hobby' | 'pro' | 'scale';
  effective_at?: string;
};

