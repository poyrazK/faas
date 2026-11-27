/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Lifecycle state of a partner invitation to verify a webhook receiver.
 */
export type APISandboxReceiverInvite = {
  run_id: string;
  scenario_revision: string;
  expires_at: string;
  status: 'pending' | 'verified' | 'ready' | 'expired';
  /**
   * Returned once when creating the invite.
   */
  token?: string;
};

