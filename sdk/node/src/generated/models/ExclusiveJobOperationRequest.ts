/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CreateJobRunRequest } from './CreateJobRunRequest.js';
/**
 * Job run intent admitted under a named exclusive-operation policy. The account and Job identity are derived from the authenticated route.
 */
export type ExclusiveJobOperationRequest = {
  policy: string;
  /**
   * Business identifier for this Job run lane; account authorization scope is derived from the authenticated route.
   */
  key: (string | number | boolean);
  /**
   * Optional identity for joining accepted Job runs only when their run intents are equivalent.
   */
  equivalence_key?: string;
  run: CreateJobRunRequest;
};

