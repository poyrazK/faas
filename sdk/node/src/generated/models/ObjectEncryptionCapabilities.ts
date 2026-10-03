/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Operator enrollment for this bucket placement. Contains no native key identity, key material or permission assertion.
 */
export type ObjectEncryptionCapabilities = {
  algorithms: Array<'AES256' | 'aws:kms' | 'aws:kms:dsse'>;
  key_ids: Array<string>;
};

