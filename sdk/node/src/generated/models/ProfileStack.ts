/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProfileSourceLocation } from './ProfileSourceLocation.js';
/**
 * One frame in a bounded CPU call-path tree.
 */
export type ProfileStack = {
  name: string;
  file?: string;
  line?: number;
  source?: ProfileSourceLocation;
  cpu_seconds: number;
  children?: Array<ProfileStack>;
};

