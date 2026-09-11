/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Consumer credential request. The generated plaintext is returned only once.
 */
export type CreateConsumerKeyRequest = {
  name: string;
  scopes: Array<'read' | 'write' | 'admin'>;
  expires_at?: string | null;
};

