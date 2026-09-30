/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Owned app and explicit development graph for local execution.
 */
export type CreateDevBridgeRequest = {
  app: string;
  environment: string;
  developer_id: string;
  dependencies?: Array<string>;
  /**
   * Remote frontend included in the selected graph and used for the session URL.
   */
  entrypoint?: string;
};

