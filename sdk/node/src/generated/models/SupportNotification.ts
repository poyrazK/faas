/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * An actor-specific support update containing metadata only.
 */
export type SupportNotification = {
  id: string;
  sequence: number;
  ticket_id: string;
  kind: 'staff_reply' | 'customer_reply' | 'ticket_created' | 'assigned';
  created_at: string;
  read_at?: string;
};

