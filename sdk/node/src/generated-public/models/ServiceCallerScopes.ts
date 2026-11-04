/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ServiceCallScope } from './ServiceCallScope.js';
/**
 * Target-owned service authorization map from logical caller app name to allowed HTTP methods and path prefixes. When present, callers missing from the map are denied.
 */
export type ServiceCallerScopes = Record<string, ServiceCallScope>;
