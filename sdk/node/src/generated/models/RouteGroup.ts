/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteChecks } from './RouteChecks.js';
/**
 * Named policy requirements for explicit methods below a canonical literal prefix ending in slash.
 */
export type RouteGroup = {
  name: string;
  path_prefix: string;
  methods: Array<'GET' | 'HEAD' | 'POST' | 'PUT' | 'PATCH' | 'DELETE' | 'OPTIONS'>;
  require: RouteChecks;
};

