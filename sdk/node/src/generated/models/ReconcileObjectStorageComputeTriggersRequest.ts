/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObjectStorageComputeTrigger } from './ObjectStorageComputeTrigger.js';
/**
 * Revision-checked replacement of only the bucket's declarative compute triggers.
 */
export type ReconcileObjectStorageComputeTriggersRequest = {
  expected_revision: number;
  triggers: Array<ObjectStorageComputeTrigger>;
};

