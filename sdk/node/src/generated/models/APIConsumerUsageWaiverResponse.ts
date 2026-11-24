/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APIConsumerUsageWaiverRequest } from './APIConsumerUsageWaiverRequest.js';
/**
 * Approval attached to the statement revision that recorded the waiver.
 */
export type APIConsumerUsageWaiverResponse = (APIConsumerUsageWaiverRequest & {
  /**
   * Gregale account ID of the approver.
   */
  approved_by: string;
  /**
   * UTC time Gregale recorded the approval.
   */
  approved_at: string;
});

