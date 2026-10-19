/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Revision-checked replacement credential for the same source database identity.
 */
export type RotateDataConnectionRequest = {
  expected_revision: number;
  /**
   * Replacement verified-TLS URL with an explicit password for the existing source host, database and username. addresses must be operator-qualified.
   */
  connection_url: string;
};

