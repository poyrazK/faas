/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Upload policy declaration for POST /uploads/{name}.
 */
export type CreateObjectUploadRouteRequest = {
  name: string;
  bucket_id: string;
  key_prefix?: string;
  max_bytes?: number;
  allowed_content_types?: Array<string>;
  enabled?: boolean;
};

