/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Path, query, and header validation for a kind=validate rule
 * (ADR-091 amendment: request parameters). Each schema is a JSON
 * Schema object whose properties are parameter names; property types
 * must be string, integer, number, boolean, or an array of those.
 * Request values are converted to the declared type before
 * validation, and a value that does not convert stays a string so
 * the schema reports it. All three are checked before the body is
 * read, in the rule's validate_mode; errors name the location
 * (`path/id`, `query/limit`, `headers/x-tenant`).
 *
 */
export type EdgeRuleValidateParameters = {
  /**
   * OpenAPI path template, such as `/users/{id}`. Required with
   * `path`. The rule's `match_path` must equal the template with
   * each placeholder replaced by `?*` (`/users/?*`), and each
   * segment may hold at most one placeholder.
   *
   */
  path_template?: string;
  /**
   * Object schema whose properties are exactly the template's placeholders.
   */
  path?: Record<string, any>;
  /**
   * Object schema over every query parameter; a repeated name
   * becomes an array, and `additionalProperties: false` rejects
   * unknown parameters.
   *
   */
  query?: Record<string, any>;
  /**
   * Object schema over the headers it declares, named in lowercase.
   * Comma-separated values become an array for array properties.
   *
   */
  headers?: Record<string, any>;
};

