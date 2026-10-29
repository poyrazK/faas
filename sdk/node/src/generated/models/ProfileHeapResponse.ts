/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProfileCapture } from './ProfileCapture.js';
import type { ProfileCaptureFunction } from './ProfileCaptureFunction.js';
import type { ProfileCaptureStack } from './ProfileCaptureStack.js';
import type { ProfileQuery } from './ProfileQuery.js';
import type { ProfileSource } from './ProfileSource.js';
/**
 * Continuous live heap near query.end.
 */
export type ProfileHeapResponse = {
  query: ProfileQuery;
  capture?: ProfileCapture;
  kind: 'heap';
  unit: 'bytes';
  total: number;
  functions: Array<ProfileCaptureFunction>;
  flamegraph: ProfileCaptureStack;
  empty: boolean;
  source?: ProfileSource;
};

