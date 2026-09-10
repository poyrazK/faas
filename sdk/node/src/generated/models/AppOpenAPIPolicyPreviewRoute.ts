/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppOpenAPIPolicyPreviewRule } from './AppOpenAPIPolicyPreviewRule.js';
/**
 * One declared/observed path-method row with policy coverage and drift status.
 */
export type AppOpenAPIPolicyPreviewRoute = {
  path: string;
  method: 'get' | 'put' | 'post' | 'delete' | 'options' | 'head' | 'patch' | 'trace';
  status: 'matched' | 'declared_only' | 'observed_only';
  declared: boolean;
  observed: boolean;
  /**
   * True when at least one enabled edge rule matches this path and method.
   */
  covered: boolean;
  rules?: Array<AppOpenAPIPolicyPreviewRule>;
};

