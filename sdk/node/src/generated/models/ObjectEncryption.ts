/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * PUT-only owned encryption selection. KMS requires an enrolled Gregale key reference; bucket keys apply only to aws:kms. Context is canonical base64 of a bounded JSON object with unique string entries.
 */
export type ObjectEncryption = {
  algorithm: 'AES256' | 'aws:kms' | 'aws:kms:dsse';
  /**
   * Owned arn:gregale:kms reference; required for KMS and forbidden for AES256.
   */
  key_id?: string;
  bucket_key_enabled?: boolean;
  context?: string;
};

