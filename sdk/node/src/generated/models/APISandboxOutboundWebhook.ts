/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APISandboxJSONAssertion } from './APISandboxJSONAssertion.js';
/**
 * Check a managed async job completion callback to a sandbox-owned receiver.
 */
export type APISandboxOutboundWebhook = {
  /**
   * Return HTTP 503 on the first verified delivery and HTTP 204 on the retry.
   */
  retry_once?: boolean;
  /**
   * Assertions on the job result carried by the callback.
   */
  assertions?: Array<APISandboxJSONAssertion>;
};

