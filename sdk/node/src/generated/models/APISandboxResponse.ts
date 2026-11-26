/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APISandboxFailure } from './APISandboxFailure.js';
import type { APISandboxPartnerResponse } from './APISandboxPartnerResponse.js';
import type { APISandboxScenarioReceipt } from './APISandboxScenarioReceipt.js';
import type { APISandboxVerification } from './APISandboxVerification.js';
/**
 * Status and access details for a retained release's isolated API copy.
 */
export type APISandboxResponse = {
  /**
   * The leading requests executed by the partner before the platform checks final state.
   */
  partner_step_count?: number;
  id: string;
  source_deployment_id: string;
  deployment_id?: string;
  status: 'preparing_database' | 'running_migrations' | 'starting_release' | 'verifying_api' | 'live' | 'failed' | 'tearing_down' | 'ended';
  url: string;
  openapi_url: string;
  expires_at: string;
  smoke_path?: string;
  failure?: APISandboxFailure;
  verification?: APISandboxVerification;
  scenario?: APISandboxScenarioReceipt;
  partner?: APISandboxPartnerResponse;
};

