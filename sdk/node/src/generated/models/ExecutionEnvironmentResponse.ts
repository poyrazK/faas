/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type ExecutionEnvironmentResponse = {
  id: string;
  runtime: 'node22' | 'node24' | 'python312' | 'python313';
  status: 'queued' | 'building' | 'built' | 'imaging' | 'ready' | 'failed' | 'retiring' | 'deleted';
  recipe_digest: string;
  image_digest?: string;
  artifact_bytes?: number;
  failure_code?: string;
  created_at: string;
};

