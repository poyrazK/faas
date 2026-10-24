/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Per-bucket count of CVEs in Grype's closed vocabulary
 * (CRITICAL|HIGH|MEDIUM|LOW|UNKNOWN). Negligible collapses into LOW
 * (matches the existing pkg/imaged.grype.go::normalizeGrypeSeverity
 * convention). All fields present without omitempty so the JSON shape
 * is uniform — the dashboard reads counts without nil checks.
 *
 */
export type SeverityCounts = {
  critical: number;
  high: number;
  medium: number;
  low: number;
  unknown: number;
};

