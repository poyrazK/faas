/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Previous routing record at the production hostname. No URLs, credentials or TXT records.
 */
export type MigrationDNSRecord = {
  type: 'A' | 'AAAA' | 'CNAME';
  value: string;
  ttl: number;
};

