/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Exact object operation to authorize for a short time.
 */
export type ObjectSignRequest = {
  method: 'GET' | 'PUT';
  key: string;
  expires_in?: number;
  /**
   * Required for PUT; forbidden for GET.
   */
  size_bytes?: number;
  /**
   * PUT only.
   */
  content_type?: string;
};

