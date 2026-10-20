/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ReleaseFreeze } from './ReleaseFreeze.js';
/**
 * Whether a production release of the app would be refused now.
 */
export type ReleaseFreezeStatus = {
  app: string;
  frozen: boolean;
  /**
   * The freezes refusing a production release now.
   */
  active: Array<ReleaseFreeze>;
  next?: ReleaseFreeze;
};

