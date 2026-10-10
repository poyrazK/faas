/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Provider credentials and enablement settings for realtime push delivery.
 */
export type ManagedRealtimePushProviderRequest = {
  enabled?: boolean;
  config: {
    provider: 'fcm' | 'apns' | 'webpush';
    project_id?: string;
    /**
     * FCM service-account JSON with Firebase Messaging permission.
     */
    service_account_json?: Record<string, any>;
    team_id?: string;
    key_id?: string;
    topic?: string;
    /**
     * APNs PKCS8 PEM key or Web Push unpadded base64url P-256 private scalar.
     */
    private_key?: string;
    sandbox?: boolean;
    /**
     * VAPID mailto or https contact URI.
     */
    subject?: string;
    title?: string;
    body?: string;
  };
};

