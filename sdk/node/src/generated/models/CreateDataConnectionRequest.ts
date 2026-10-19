/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Source registration containing a verified-TLS PostgreSQL URL for sealing.
 */
export type CreateDataConnectionRequest = {
  name: string;
  /**
   * PostgreSQL URL with sslmode=verify-full. Stored encrypted and never returned. Source host and addresses must be operator-qualified.
   */
  connection_url: string;
};

