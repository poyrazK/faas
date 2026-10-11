/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProfileSourceLocation } from './ProfileSourceLocation.js';
/**
 * Sampled self and inclusive CPU seconds for one source function.
 */
export type ProfileFunction = {
  name: string;
  file?: string;
  line?: number;
  source?: ProfileSourceLocation;
  self_cpu_seconds: number;
  total_cpu_seconds: number;
};

