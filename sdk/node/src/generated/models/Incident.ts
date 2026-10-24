/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * An alert rule's firing period, a route monitor incident, or a platform incident.
 */
export type Incident = {
  id: string;
  source: 'alert' | 'route_monitor' | 'platform';
  status: 'open' | 'resolved';
  title: string;
  /**
   * The app's slug; absent for account-wide alerts and platform incidents.
   */
  app?: string;
  opened_at: string;
  resolved_at?: string;
  /**
   * An alert incident's rule.
   */
  rule_id?: string;
  /**
   * An alert incident's rule metric.
   */
  metric?: string;
  /**
   * The deployment a route monitor incident watched.
   */
  deployment_id?: string;
  /**
   * A platform incident's status page impact.
   */
  impact?: string;
  /**
   * When someone in the account acknowledged the incident.
   */
  acknowledged_at?: string;
  /**
   * Who acknowledged it, an email or api_key:<id>.
   */
  acknowledged_by?: string;
};

