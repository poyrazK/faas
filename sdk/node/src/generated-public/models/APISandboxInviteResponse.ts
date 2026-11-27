/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APISandboxResponse } from './APISandboxResponse.js';
/**
 * Sandbox-specific bearer key and guide for using the isolated API.
 */
export type APISandboxInviteResponse = {
  sandbox: APISandboxResponse;
  /**
   * Sandbox-only bearer credential.
   */
  token: string;
};

