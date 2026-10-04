/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObjectEncryption } from './ObjectEncryption.js';
/**
 * Final object identity, total size and optional owned encryption frozen at initiation for a resumable upload.
 */
export type CreateObjectMultipartUploadRequest = {
  key: string;
  size_bytes: number;
  content_type?: string;
  encryption?: ObjectEncryption;
};

