/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type ApplicationStandardSource = {
  assignment_id?: string;
  scope_id?: string;
  standard_id: string;
  version: number;
  scope: 'organization' | 'project' | 'application';
  mode: 'default' | 'mandatory' | 'restricted';
  override: 'none' | 'narrow' | 'extend';
  exception_id?: string;
};

