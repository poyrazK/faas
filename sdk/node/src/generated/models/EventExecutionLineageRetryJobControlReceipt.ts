/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Immutable original successful control outcome with command identity, progress, and trusted actor attribution.
 */
export type EventExecutionLineageRetryJobControlReceipt = {
  /**
   * Server-resolved original command actor; absent on legacy receipts.
   */
  actor_kind?: 'account' | 'credential' | 'internal';
  /**
   * Account UUID for a session, credential UUID for a bearer, or store for an internal call. Never a token value or session ID.
   */
  actor_id?: string;
  /**
   * Original server-resolved authentication type; absent on legacy receipts.
   */
  credential_type?: 'session' | 'api_key' | 'deploy_token' | 'platform_tenant_token' | 'internal';
  command_id: string;
  sequence: number;
  job_request_id: string;
  subscription_id: string;
  app_id: string;
  action: 'pause' | 'resume' | 'cancel';
  expected_created_at: string;
  /**
   * Immutable job state observed by the original command.
   */
  result_status: 'pending' | 'paused' | 'completed' | 'cancelled';
  processed: number;
  selected: number;
  job_updated_at: string;
  recorded_at: string;
  retain_until: string;
};

