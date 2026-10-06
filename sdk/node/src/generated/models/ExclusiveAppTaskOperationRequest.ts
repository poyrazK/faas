/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CreateAppTaskRequest } from './CreateAppTaskRequest.js';
/**
 * Deployment-attached app task intent admitted under a named exclusive-operation policy.
 */
export type ExclusiveAppTaskOperationRequest = {
  policy: string;
  /**
   * Business identifier for this deployment-attached task lane; account and customer authorization scope comes from trusted platform context.
   */
  key: (string | number | boolean);
  /**
   * Optional identity for joining accepted app tasks only when their task intents are equivalent.
   */
  equivalence_key?: string;
  task: CreateAppTaskRequest;
};

