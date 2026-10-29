/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Consumer credential request. The generated plaintext is returned only once.
 */
export type CreateConsumerKeyRequest = {
  name: string;
  /**
   * read allows GET, write and admin allow every method; billing lets the consumer read its own billing and grants no app access.
   */
  scopes: Array<'read' | 'write' | 'admin' | 'billing'>;
  expires_at?: string | null;
};

