/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Consumer credential projection. key is present only on the create response.
 */
export type ConsumerKeyResponse = {
  id: string;
  consumer_id?: string;
  name: string;
  /**
   * Public lookup prefix; not secret.
   */
  prefix: string;
  scopes: Array<'read' | 'write' | 'admin'>;
  created_at: string;
  expires_at?: string | null;
  last_used_at?: string | null;
  revoked_at?: string | null;
  /**
   * Plaintext credential, returned exactly once from POST.
   */
  key?: string;
};

