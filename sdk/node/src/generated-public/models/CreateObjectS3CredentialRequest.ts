/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Label and least-privilege access level for a new bucket-scoped S3 credential.
 */
export type CreateObjectS3CredentialRequest = {
  label: string;
  permission: 'read' | 'write' | 'read_write';
};

