/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DeclaredRoute } from './DeclaredRoute.js';
/**
 * Effective declared-route contract and whether it is environment-owned.
 */
export type ProjectEnvironmentRoutePolicyResponse = {
  ownership: 'application' | 'environment';
  only_allow_declared_routes: boolean;
  declared_routes: Array<DeclaredRoute>;
};

