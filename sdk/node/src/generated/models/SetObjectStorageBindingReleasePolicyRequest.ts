/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObjectStorageReleaseRequirements } from './ObjectStorageReleaseRequirements.js';
import type { SetBindingReleasePolicyRequest } from './SetBindingReleasePolicyRequest.js';
/**
 * Revision-guarded release policy replacement with explicit required object-storage canary modes.
 */
export type SetObjectStorageBindingReleasePolicyRequest = (SetBindingReleasePolicyRequest & {
  object_storage_requirements: ObjectStorageReleaseRequirements;
});

