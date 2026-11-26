/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
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

