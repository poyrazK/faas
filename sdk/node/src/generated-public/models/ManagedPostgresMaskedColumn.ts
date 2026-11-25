/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Masking rule for a column in the copied database.
 */
export type ManagedPostgresMaskedColumn = {
  name: string;
  /**
   * How values in this selected column are copied.
   */
  action: 'keep' | 'email' | 'token' | 'null';
};

