/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PreflightLevel } from './PreflightLevel.js';
/**
 * One actionable observation about the source. `detail` says what was
 * observed; `remedy` says what to change about it.
 *
 */
export type PreflightFinding = {
  /**
   * Stable machine-readable finding code.
   */
  code: string;
  level: PreflightLevel;
  title: string;
  /**
   * What was observed in the source.
   */
  detail: string;
  /**
   * What to change. Empty for informational findings.
   */
  remedy?: string;
  /**
   * Repository-relative paths the finding came from. Never file contents.
   */
  sources?: Array<string>;
};

