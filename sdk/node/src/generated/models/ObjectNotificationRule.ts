/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Proven mutation routing to a Gregale function or existing enabled queue binding. Prefix and suffix are decoded strings; overlapping filters for the same event are rejected. IDs are generated when omitted.
 */
export type ObjectNotificationRule = {
  id?: string;
  /**
   * arn:gregale:lambda:REGION:ACCOUNT:function:APP_UUID or arn:gregale:sqs:REGION:ACCOUNT:APP_UUID/QUEUE_NAME
   */
  destination: string;
  events: Array<'s3:ObjectCreated:*' | 's3:ObjectCreated:Put' | 's3:ObjectCreated:Copy' | 's3:ObjectCreated:CompleteMultipartUpload' | 's3:ObjectRemoved:*' | 's3:ObjectRemoved:Delete' | 's3:ObjectRemoved:DeleteMarkerCreated' | 's3:LifecycleExpiration:*' | 's3:LifecycleExpiration:Delete' | 's3:LifecycleExpiration:DeleteMarkerCreated'>;
  prefix?: string;
  suffix?: string;
};

