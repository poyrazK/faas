/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProfileSourceLocation } from './ProfileSourceLocation.js';
/**
 * One frame in a bounded call-path tree.
 */
export type ProfileCaptureStack = {
  name: string;
  file?: string;
  line?: number;
  source?: ProfileSourceLocation;
  value: number;
  children?: Array<ProfileCaptureStack>;
};

