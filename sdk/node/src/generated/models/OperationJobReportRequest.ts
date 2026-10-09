/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationReportRequest } from './OperationReportRequest.js';
/**
 * Stable native task report carrying progress or typed private output.
 */
export type OperationJobReportRequest = {
  report_id: string;
  progress?: OperationReportRequest;
  /**
   * JSON result matching the immutable output schema.
   */
  result?: any;
};

