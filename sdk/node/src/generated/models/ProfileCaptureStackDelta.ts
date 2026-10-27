/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProfileSourceLocation } from './ProfileSourceLocation.js';
/**
 * One call path in both captures.
 */
export type ProfileCaptureStackDelta = {
  name: string;
  file?: string;
  line?: number;
  source?: ProfileSourceLocation;
  baseline: number;
  candidate: number;
  delta: number;
  children?: Array<ProfileCaptureStackDelta>;
};

