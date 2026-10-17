/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Durable GitHub ticket evidence and Gregale attachment state for a native creation operation.
 */
export type NativeIssueTicketResult = {
  request_id: string;
  state: 'created' | 'linked';
  ticket_url: string;
  link_id?: string;
};

