/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One signal delivery. waiting reports whether the operation was waiting for this event; otherwise the run holds it for a later wait.
 */
export type OperationSignalReceipt = {
  operation_id: string;
  signal_id: string;
  event: string;
  duplicate: boolean;
  waiting: boolean;
};

