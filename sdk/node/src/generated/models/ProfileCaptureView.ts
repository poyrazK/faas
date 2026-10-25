/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProfileCapture } from './ProfileCapture.js';
import type { ProfileCaptureFunction } from './ProfileCaptureFunction.js';
import type { ProfileCaptureStack } from './ProfileCaptureStack.js';
import type { ProfileSource } from './ProfileSource.js';
/**
 * One profile kind merged across processes. unit is nanoseconds for cpu, bytes for heap and count (goroutines or threads) for stacks. Node and Python heap captures report memory allocated during the window and still live at its end; Go reports the sampled live heap.
 */
export type ProfileCaptureView = {
  capture: ProfileCapture;
  kind: 'cpu' | 'heap' | 'stacks' | 'block' | 'mutex';
  unit: 'nanoseconds' | 'bytes' | 'count';
  total: number;
  functions: Array<ProfileCaptureFunction>;
  flamegraph: ProfileCaptureStack;
  empty: boolean;
  source?: ProfileSource;
};

