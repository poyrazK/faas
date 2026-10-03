/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Explicit regional API origin and app identity. Use independent regional credentials when calling that API.
 */
export type GlobalAppResolutionResponse = {
  global_app_id: string;
  cell_id: string;
  region: string;
  api_base: string;
  regional_app_id: string;
  regional_app_slug: string;
  cell_generation: number;
};

