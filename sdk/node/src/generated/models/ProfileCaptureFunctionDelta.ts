/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProfileSourceLocation } from './ProfileSourceLocation.js';
/**
 * One function's self cost in both captures; named functions aggregate across lines.
 */
export type ProfileCaptureFunctionDelta = {
  name: string;
  file?: string;
  line?: number;
  source?: ProfileSourceLocation;
  baseline: number;
  candidate: number;
  delta: number;
};

