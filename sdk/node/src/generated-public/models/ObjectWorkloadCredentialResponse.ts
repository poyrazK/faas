/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A workload S3 credential in the AWS container credential shape, plus where the workload may write.
 */
export type ObjectWorkloadCredentialResponse = {
  AccessKeyId: string;
  SecretAccessKey: string;
  Token: string;
  Expiration: string;
  Bucket: string;
  KeyPrefix?: string;
  Endpoint?: string;
  Region?: string;
};

