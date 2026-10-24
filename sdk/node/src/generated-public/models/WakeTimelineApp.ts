/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Slim per-app identification embedded in `AppWakeTimelineResponse`.
 * Carries only the fields the dashboard SPA needs for the
 * wake-timeline header (slug + app_id). The wider
 * pkg/dashboard.AppListItem type carries template-specific
 * glyph/badge fields (SLO badge, StateBadge*, QuotaLabel) that
 * don't belong on the wire.
 *
 */
export type WakeTimelineApp = {
  app_id: string;
  /**
   * DNS-safe app slug (matches apps.slug).
   */
  slug: string;
  /**
   * Optional deployment status (active/paused). Empty until a deployment is bound.
   */
  status?: string;
  /**
   * Optional public URL once a deployment is bound.
   */
  url?: string;
};

