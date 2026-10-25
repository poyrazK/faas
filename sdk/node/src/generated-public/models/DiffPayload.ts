/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DiffBreak } from './DiffBreak.js';
import type { DiffChange } from './DiffChange.js';
/**
 * Inner diff object the engine produces. Slug + Plan +
 * Changes + Breaks. Wrapped by [DiffResponse] so a CI
 * consumer reading `.diff.changes` and a CLI consumer
 * reading the top-level keys agree.
 *
 */
export type DiffPayload = {
  slug: string;
  /**
   * Customer's subscription tier (echoed from acct.Plan).
   */
  plan?: 'free' | 'hobby' | 'pro' | 'scale';
  changes: Array<DiffChange>;
  breaks: Array<DiffBreak>;
};

