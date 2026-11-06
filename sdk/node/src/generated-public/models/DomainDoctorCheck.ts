/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One row of the doctor report. Stable name tokens (dns_record / points_to_gregale / tls_certificate / caa_permits / ipv6_conflict) so the CLI can filter by name without parsing the human Detail field. Remediation is the exact record to change when status is fail — the load-bearing field for the activation drop-off.
 */
export type DomainDoctorCheck = {
  name: 'dns_record' | 'points_to_gregale' | 'tls_certificate' | 'caa_permits' | 'ipv6_conflict';
  status: 'ok' | 'fail' | 'pending' | 'na';
  detail: string;
  observed?: string;
  remediation?: string;
  checked_at?: string;
};

