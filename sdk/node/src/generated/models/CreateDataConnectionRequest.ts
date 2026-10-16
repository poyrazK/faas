/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type CreateDataConnectionRequest = {
  name: string;
  /**
   * PostgreSQL URL with sslmode=verify-full. Stored encrypted and never returned. Source host and addresses must be operator-qualified.
   */
  connection_url: string;
};

