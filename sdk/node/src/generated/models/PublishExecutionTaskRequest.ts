/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CreateExecutionRequest } from './CreateExecutionRequest.js';
/**
 * Source, execution configuration and contracts for a new immutable task version.
 */
export type PublishExecutionTaskRequest = {
  name: string;
  version: number;
  definition: CreateExecutionRequest;
  agent_endpoint_ids?: Array<string>;
};

