/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APISandboxRunStep } from './APISandboxRunStep.js';
export type APISandboxRunReport = {
  id: string;
  source_deployment_id: string;
  sandbox_deployment_id?: string;
  scenario_revision: string;
  scenario_name: string;
  mode: 'automated' | 'partner';
  partner_step_count?: number;
  partner_webhook?: boolean;
  partner_outcome_until?: string;
  status: 'queued' | 'running' | 'awaiting_partner' | 'awaiting_partner_outcome' | 'checking_state' | 'passed' | 'failed' | 'interrupted';
  steps: Array<APISandboxRunStep>;
  failure_code?: string;
  created_at: string;
  started_at?: string;
  finished_at?: string;
};

