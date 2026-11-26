/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APISandboxPartnerResponse } from './APISandboxPartnerResponse.js';
export type APISandboxPartnerInviteResponse = {
  partner: APISandboxPartnerResponse;
  /**
   * One-time invite token exchanged through the sandbox redemption endpoint.
   */
  token: string;
};

