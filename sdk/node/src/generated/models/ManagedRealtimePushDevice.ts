/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Registered device metadata with delivery tokens omitted.
 */
export type ManagedRealtimePushDevice = {
  device: string;
  provider: 'fcm' | 'apns' | 'webpush';
  enabled: boolean;
  version: number;
  updated_at: string;
};

