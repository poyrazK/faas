/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Repo visible to the user's GitHub App installation, as
 * returned by githubd's `/user/installations/{id}/repositories`.
 * Carries only the fields the dashboard bind picker renders —
 * no nested owner object (the install URL already disambiguates).
 *
 */
export type RepoResponse = {
  id: number;
  full_name: string;
  default_branch: string;
  private: boolean;
};

