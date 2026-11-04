/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One S3 CORSRule. Origins are "*" or an http(s) origin with at most one "*"; header patterns allow one "*".
 */
export type ObjectBucketCORSRule = {
  id?: string;
  allowed_origins: Array<string>;
  allowed_methods: Array<'GET' | 'PUT' | 'POST' | 'DELETE' | 'HEAD'>;
  allowed_headers?: Array<string>;
  expose_headers?: Array<string>;
  max_age_seconds?: number;
};

