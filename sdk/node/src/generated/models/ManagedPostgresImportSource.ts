/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Connection details for a PostgreSQL source to inspect or import.
 */
export type ManagedPostgresImportSource = {
  /**
   * Public PostgreSQL endpoint with a certificate valid for this name.
   */
  host: string;
  port: number;
  database: string;
  user: string;
  password: string;
  skip_extensions?: Array<string>;
};

