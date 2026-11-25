/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FlagEdgeAction } from './FlagEdgeAction.js';
/**
 * One routed path: /page matches exactly, /prefix* matches the prefix and everything under it. Variants without an action serve the request unchanged.
 */
export type FlagEdgeRoute = {
  path: string;
  /**
   * Variant key to the action requests decided as that variant get.
   */
  variants: Record<string, FlagEdgeAction>;
};

