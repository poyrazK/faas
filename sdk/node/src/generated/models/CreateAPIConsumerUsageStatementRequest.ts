/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APIConsumerUsageWaiverRequest } from './APIConsumerUsageWaiverRequest.js';
/**
 * Explicit UTC-minute period to snapshot as an immutable usage statement.
 */
export type CreateAPIConsumerUsageStatementRequest = {
  period_start: string;
  period_end: string;
  /**
   * Optional administrative approval supported on app consumer statements.
   */
  waiver?: APIConsumerUsageWaiverRequest;
};

