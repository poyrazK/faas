/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ApplicationStandardException } from './ApplicationStandardException.js';
/**
 * Page of historical exceptions and their status evaluated at the server as_of timestamp.
 */
export type ApplicationStandardExceptionList = {
  exceptions: Array<ApplicationStandardException>;
  next_page_after?: string;
  as_of: string;
};

