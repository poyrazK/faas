/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProfileCapture } from './ProfileCapture.js';
import type { ProfileCaptureFunctionDelta } from './ProfileCaptureFunctionDelta.js';
import type { ProfileCaptureStackDelta } from './ProfileCaptureStackDelta.js';
/**
 * One kind compared between two ready captures of an app (ADR-1155). Values keep the kind's unit and are not rescaled.
 */
export type ProfileCaptureDiff = {
  baseline: ProfileCapture;
  candidate: ProfileCapture;
  kind: 'cpu' | 'heap' | 'stacks' | 'block' | 'mutex';
  unit: 'nanoseconds' | 'bytes' | 'count';
  baseline_total: number;
  candidate_total: number;
  delta: number;
  functions: Array<ProfileCaptureFunctionDelta>;
  flamegraph: ProfileCaptureStackDelta;
  warnings: Array<string>;
};

