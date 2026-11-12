/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Durable transaction receipt; UUID idempotency keys replay indefinitely while current scope access remains valid.
 */
export type SupportSubmission = {
  ticket_id: string;
  submission_id: string;
  revision: number;
};

