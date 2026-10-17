/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A consumer the gateway refuses because of its oldest overdue invoice.
 */
export type APIConsumerOverdueHoldResponse = {
  consumer_id: string;
  export_id: string;
  due_at: string;
  since: string;
};

