/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Present when one route holds at least 80% of at least 3 memory peaks while running in at most 30% of minutes, and the rest of the app would fit in less memory: move that route to its own app and size the app down (kind memory). When no memory split qualifies, the same rule over CPU peaks suggests moving the route for isolation; both apps keep the current size (kind cpu).
 */
export type RouteResourceSplit = {
  /**
   * memory sizes both apps for what they need; cpu isolates a route whose CPU bursts slow the app's other routes.
   */
  kind: 'memory' | 'cpu';
  route: string;
  method?: string;
  /**
   * Edge-rule path match for the route; path parameters become one-segment wildcards.
   */
  path_glob: string;
  /**
   * Suggested size of the new app: the route's p95 peak plus 25%, rounded up to 64 MB.
   */
  route_ram_mb: number;
  /**
   * The route needs more memory than the plan's largest app.
   */
  route_ram_over_plan?: boolean;
  current_ram_mb: number;
  /**
   * Suggested size of the app without the route: its median peak plus the p95 rise without the route, plus 25%, rounded up to 64 MB.
   */
  main_ram_mb: number;
  high_memory_share: number;
  presence_share: number;
  high_memory_minutes: number;
  /**
   * Share of attributed CPU peaks the route was in flight for; set on cpu splits.
   */
  high_cpu_share?: number;
  /**
   * CPU peaks the route was in flight for; set on cpu splits.
   */
  high_cpu_minutes?: number;
  reason: string;
};

