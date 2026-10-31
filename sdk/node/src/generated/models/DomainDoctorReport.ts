/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DomainDoctorCheck } from './DomainDoctorCheck.js';
/**
 * Per-domain doctor report. Carries 5 Render-style checks (dns_record, points_to_gregale, tls_certificate, caa_permits, ipv6_conflict) plus the durable row's app_id and observed_at. `stale:true` means the cached observation row was older than FAAS_DOMAIN_DOCTOR_TTL_SECONDS (default 300) when the handler ran a synchronous re-probe.
 */
export type DomainDoctorReport = {
  domain: string;
  app_id: string;
  stale?: boolean;
  observed_at: string;
  healthy: boolean;
  checks: Array<DomainDoctorCheck>;
};

