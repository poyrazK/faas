/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RetryPolicyDTO } from './RetryPolicyDTO.js';
/**
 * A CloudEvents-compatible application-inbox message.
 */
export type SendAppMessageRequest = {
  /**
   * Generated when omitted.
   */
  id?: string;
  source?: string;
  type: string;
  /**
   * Server time when omitted.
   */
  time?: string;
  data_content_type?: 'application/json';
  /**
   * Any valid JSON value delivered inside the CloudEvents envelope.
   */
  data: any;
  queue_name?: string;
  retry_policy?: RetryPolicyDTO;
};

