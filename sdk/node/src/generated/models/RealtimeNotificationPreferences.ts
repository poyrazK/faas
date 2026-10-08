/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type RealtimeNotificationPreferences = {
  /**
   * Shared per-principal provider-delivery quota across devices. Null or omission disables it. Digests consume one slot.
   */
  rate_limit?: any | null;
  /**
   * Allow urgent alerts to bypass quiet hours and digest delays; mute settings still apply.
   */
  allow_urgent_bypass?: boolean;
  /**
   * Immediate, five-minute or hourly UTC delivery windows.
   */
  digest_interval_seconds?: 0 | 300 | 3600;
  /**
   * Combine eligible alerts released after quiet hours; null inherits true.
   */
  summarize_quiet_hours?: boolean | null;
  /**
   * Master push switch for this principal.
   */
  enabled: boolean;
  /**
   * Unlisted categories are enabled. False cancels push for this category.
   */
  categories?: any | null;
  /**
   * Null or omitted selects all registered devices; empty array selects none.
   */
  devices?: any[] | null;
  /**
   * Daily half-open quiet interval; start and end must differ. Omit or null to disable.
   */
  quiet_hours?: any | null;
};

