/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteGroup } from './RouteGroup.js';
import type { RoutePublicException } from './RoutePublicException.js';
import type { RouteRequirement } from './RouteRequirement.js';
/**
 * Version 1 requires 1..500 concrete routes. Version 2 assigns every captured operation to groups, concrete routes, or public exceptions; overlapping groups are conjunctive.
 */
export type RouteRequirementsConfig = {
  version: 1 | 2;
  routes?: Array<RouteRequirement>;
  groups?: Array<RouteGroup>;
  public?: Array<RoutePublicException>;
};

