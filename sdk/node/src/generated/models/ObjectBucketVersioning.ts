/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type ObjectBucketVersioning = {
  bucket_id: string;
  desired_status: '' | 'Enabled' | 'Suspended';
  observed_status: '' | 'Enabled' | 'Suspended';
  state: 'ready' | 'waiting' | 'propagating' | 'inventory';
  revision: number;
  versions_required: boolean;
  propagation_until?: string;
  capacity_job_id?: string;
  last_error_code?: string;
  updated_at: string;
};

