/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationSubject } from './OperationSubject.js';
/**
 * One named event for the authenticated customer's own active operations of a definition about one business subject (ADR-20261017100853).
 */
export type PlatformTenantSubjectSignalRequest = {
  /**
   * Operation definition whose app
   */
  definition_id: string;
  subject: OperationSubject;
  signal_id: string;
  event: string;
  /**
   * JSON each of the customer's reached operations receives as its wait step output; at most 64 KiB.
   */
  payload?: any;
};

