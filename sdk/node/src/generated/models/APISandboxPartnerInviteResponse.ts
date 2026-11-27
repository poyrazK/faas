/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APISandboxPartnerResponse } from './APISandboxPartnerResponse.js';
/**
 * One-time credential used to exchange an invitation for a partner key.
 */
export type APISandboxPartnerInviteResponse = {
  partner: APISandboxPartnerResponse;
  /**
   * One-time invite token exchanged through the sandbox redemption endpoint.
   */
  token: string;
};

