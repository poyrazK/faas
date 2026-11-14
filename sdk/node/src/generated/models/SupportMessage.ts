/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One public conversation message, with a marker in place of redacted content.
 */
export type SupportMessage = {
  id: string;
  sequence: number;
  author: 'customer' | 'staff';
  body: string;
  redacted: boolean;
  created_at: string;
};

