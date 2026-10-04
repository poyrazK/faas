/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObjectEncryption } from './ObjectEncryption.js';
/**
 * Owned desired and verified policies. Empty encryption means no owned override; providers may retain baseline encryption. New implicit writes require state ready.
 */
export type ObjectBucketEncryption = {
  bucket_id: string;
  state: 'waiting' | 'applying' | 'ready';
  revision: number;
  /**
   * Verified active default for new implicit writes.
   */
  encryption?: ObjectEncryption;
  /**
   * Requested default awaiting native verification.
   */
  desired_encryption?: ObjectEncryption;
  updated_at: string;
};

