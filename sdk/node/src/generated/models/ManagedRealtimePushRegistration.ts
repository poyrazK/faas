/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Device registration used to route push notifications to a principal.
 */
export type ManagedRealtimePushRegistration = {
  provider: 'fcm' | 'apns' | 'webpush';
  /**
   * FCM/APNs require token; Web Push requires endpoint, p256dh and auth.
   */
  target: {
    token?: string;
    endpoint?: string;
    p256dh?: string;
    auth?: string;
  };
};

