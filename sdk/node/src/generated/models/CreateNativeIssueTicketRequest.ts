/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Explicit content and stable operation identity for native GitHub ticket creation or recovery.
 */
export type CreateNativeIssueTicketRequest = {
  request_id: string;
  repository: string;
  title: string;
  description?: string;
  recovery_url?: string;
};

