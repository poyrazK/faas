/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationSubject } from './OperationSubject.js';
/**
 * One named event for every active operation of a definition about one business subject (ADR-1197).
 */
export type OperationSubjectSignalRequest = {
  scope: string;
  operation: string;
  subject: OperationSubject;
  signal_id: string;
  event: string;
  /**
   * JSON each reached operation's wait step receives as its output; at most 64 KiB.
   */
  payload?: any;
};

