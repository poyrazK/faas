/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationDeliveryAttempt } from './OperationDeliveryAttempt.js';
/**
 * Newest-first attempt page with an operation-bound continuation cursor.
 */
export type OperationDeliveryAttemptsResponse = {
  operation_id: string;
  delivery_id: string;
  attempts: Array<OperationDeliveryAttempt>;
  next_cursor?: string;
};

