/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * An OAuth sign-in of an app's MCP server and the consumer it bills to.
 */
export type ConsumerIdentity = {
  id: string;
  consumer_id: string;
  issuer: string;
  subject: string;
  /**
   * The token's email claim; display only.
   */
  email?: string;
  created_at: string;
  updated_at: string;
};

