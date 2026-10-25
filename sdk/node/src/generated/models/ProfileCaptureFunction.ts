/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProfileSourceLocation } from './ProfileSourceLocation.js';
/**
 * Self and inclusive cost of one function in the view's unit.
 */
export type ProfileCaptureFunction = {
  name: string;
  file?: string;
  line?: number;
  source?: ProfileSourceLocation;
  self: number;
  total: number;
};

