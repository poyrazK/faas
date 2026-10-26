/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DiffPayload } from './DiffPayload.js';
/**
 * Wire envelope for POST /v1/apps/{slug}/diff and
 * `gregale deploy --diff --json`. Wraps the [DiffPayload]
 * plus the Blocking bit so a CI consumer doesn't have to
 * re-scan Breaks and pick the max severity.
 *
 */
export type DiffResponse = {
  diff: DiffPayload;
  /**
   * True if any break has severity "error". Mirrors
   * [pkg/deploydiff.Diff.HasBlockingBreaks]. The exit-1
   * input for CI gates.
   *
   */
  blocking: boolean;
  slug: string;
  /**
   * Echoed at top level too — kept in sync with diff.plan.
   */
  plan?: 'free' | 'hobby' | 'pro' | 'scale';
};

