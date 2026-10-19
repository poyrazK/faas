/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DataEndpointCapacity } from './DataEndpointCapacity.js';
/**
 * New-allocation readiness at observation time. Admission atomically rechecks and reserves capacity; observations alone reserve nothing.
 */
export type DataPipelineCapacity = {
  state: 'ready' | 'insufficient_capacity' | 'needs_qualification';
  source?: DataEndpointCapacity;
  destination?: DataEndpointCapacity;
};

