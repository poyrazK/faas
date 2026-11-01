/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Every label the resource carries after a write.
 */
export type LabelsResponse = {
  /**
   * Key/value labels with Kubernetes syntax, at most 32. A key is an optional DNS-subdomain prefix and a slash, then a name of 1-63 letters, digits, '.', '_' or '-' that starts and ends with a letter or digit; a value is empty or 1-63 of the same. Keys under gregale.dev/ belong to Gregale. Labels are not secret: they appear in lists and audit events.
   */
  labels: Record<string, string>;
};

