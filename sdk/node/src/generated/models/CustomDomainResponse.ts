/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A custom domain binding: domain string, target app, verification status, and TLS provisioning state. Issue #961 / Mega-A PR-3 adds `default`, `cert_not_after`, and `cert_sans` for the `gregale domains set-default | verify | show` surface.
 */
export type CustomDomainResponse = {
  domain: string;
  app_id: string;
  challenge_token?: string | null;
  verified: boolean;
  verified_at?: string | null;
  txt_record?: string | null;
  /**
   * True when this domain is the app's default (issue #961 / Mega-A PR-3). Set via `gregale domains set-default`.
   */
  default?: boolean;
  /**
   * Issued cert NotAfter (RFC3339 UTC). Populated on verified domains; the `gregale domains show` line below the cert expiry renders against this field.
   */
  cert_not_after?: string | null;
  /**
   * Cert subject alt names (DNSNames). Useful for the `gregale domains show` listing — if the customer's CNAME points at a CDN, the SANs reveal which CDN.
   */
  cert_sans?: Array<string>;
  /**
   * Durable TLS lifecycle for the legacy custom domain (pending, issued, renewing, or failed; issue #1397 / F1). The show endpoint may temporarily return a live `dial_failed:<reason>` value when the probe cannot reach the edge.
   */
  cert_status?: string | null;
  /**
   * Durable certificate expiry timestamp recorded by the cert observer.
   */
  cert_expires_at?: string | null;
  /**
   * Most recent certificate issuance/renewal error, when cert_status is failed.
   */
  cert_last_error?: string | null;
  /**
   * Timestamp of the most recent DNS verification/doctor probe.
   */
  dns_last_checked_at?: string | null;
};

