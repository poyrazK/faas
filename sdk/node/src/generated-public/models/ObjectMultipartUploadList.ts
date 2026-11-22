/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObjectMultipartUpload } from './ObjectMultipartUpload.js';
/**
 * A page of durable provider-neutral resumable upload sessions.
 */
export type ObjectMultipartUploadList = {
  items: Array<ObjectMultipartUpload>;
  next_cursor?: string;
};

