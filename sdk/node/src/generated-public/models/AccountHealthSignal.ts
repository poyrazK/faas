/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A problem that keeps recurring, in customer wording, with the next step when there is one.
 */
export type AccountHealthSignal = {
  /**
   * Stable signal identifier.
   */
  kind: 'deploy_failing' | 'app_oom' | 'jobs_failing' | 'limit_pressure';
  /**
   * What the signal is about: an app slug, a scheduled job name or a problem code.
   */
  subject: string;
  /**
   * Short headline for the signal.
   */
  title: string;
  /**
   * What keeps happening and how to fix it.
   */
  detail: string;
  /**
   * Label of the link that fixes the signal.
   */
  action?: string;
  /**
   * Dashboard path of the page that fixes the signal.
   */
  action_url?: string;
  /**
   * When Gregale first detected the problem.
   */
  since: string;
};

