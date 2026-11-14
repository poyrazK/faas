/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One sign-in, credential or account-security change.
 */
export type SecurityActivityEvent = {
  at: string;
  /**
   * Audit event kind, e.g. auth.session.created or api_key.created.
   */
  kind: string;
  /**
   * Fixed English phrase for the kind.
   */
  summary: string;
  /**
   * Client address recorded with the event.
   */
  ip?: string;
  /**
   * Sign-in method, provider or key label, when recorded.
   */
  detail?: string;
};

