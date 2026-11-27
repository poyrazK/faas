/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObjectEncryption } from './ObjectEncryption.js';
/**
 * Upload policy declaration for POST /uploads/{name}. Optional owned encryption is captured per receipt; encrypted routes require a tracked capable provider and use the configured single PUT limit.
 */
export type CreateObjectUploadRouteRequest = {
  name: string;
  bucket_id: string;
  key_prefix?: string;
  max_bytes?: number;
  allowed_content_types?: Array<string>;
  enabled?: boolean;
  encryption?: ObjectEncryption;
};

