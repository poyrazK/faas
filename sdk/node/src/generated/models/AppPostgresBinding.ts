/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One live managed PostgreSQL binding of an app, without credential material.
 */
export type AppPostgresBinding = {
  /**
   * The binding ID that revoking the binding takes.
   */
  id: string;
  /**
   * The bound database's name.
   */
  database: string;
  scope: string;
  environment_key: string;
  access: string;
  /**
   * A binding in the deleting state no longer blocks deleting the app.
   */
  state: string;
};

