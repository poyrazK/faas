/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Where a version's calls go: an openai-format route, or a managed AI integration with a model. Exactly one of route_id and integration_id is set; on a route target model is optional and is the request's model.
 */
export type OutboundAIPromptTarget = {
  route_id?: string;
  integration_id?: string;
  model?: string;
};

