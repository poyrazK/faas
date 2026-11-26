/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * An applied route split and where it is.
 */
export type RouteSplit = {
  id: string;
  slug: string;
  /**
   * The route's app.
   */
  target_slug: string;
  kind: 'memory' | 'cpu';
  route: string;
  method?: string;
  path_glob: string;
  route_ram_mb: number;
  main_ram_mb: number;
  /**
   * The app's size before the split; undo restores it.
   */
  previous_ram_mb: number;
  main_resized: boolean;
  edge_rule_ids: Array<string>;
  phase: 'creating' | 'deploying' | 'rule_live' | 'done' | 'rolled_back' | 'undone';
  warning?: string;
  error?: string;
  created_at: string;
  updated_at: string;
};

