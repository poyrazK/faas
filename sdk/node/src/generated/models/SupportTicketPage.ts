/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { SupportTicket } from './SupportTicket.js';
/**
 * Creation-ordered page of at most fifty cases; pass next_cursor as cursor.
 */
export type SupportTicketPage = {
  items: Array<SupportTicket>;
  next_cursor?: string;
};

