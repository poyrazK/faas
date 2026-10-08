/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type ManagedRealtimeScheduleRequest = {
  /**
   * Optional immutable channel-scoped group label; at most 128 UTF-8 bytes.
   */
  group?: string;
  /**
   * AND predicates over reducer state in this channel; at most 4 KiB encoded. Each predicate has a key and exactly one operator.
   */
  conditions?: Array<{
    key: string;
    field?: string;
    /**
     * Entity existence; field must be omitted.
     */
    exists?: boolean;
    /**
     * Any JSON value, compared structurally; requires field.
     */
    equals?: any;
    lt?: number;
    lte?: number;
    gt?: number;
    gte?: number;
  }>;
  /**
   * Requires conditions; retry follows the configured attempt budget.
   */
  on_condition_failure?: 'skip' | 'retry';
  /**
   * Fixed delay after each successful occurrence; omit for one-time publication.
   */
  interval_seconds?: number;
  /**
   * Optional completed-slot limit (published or intentionally skipped); zero is unlimited.
   */
  max_occurrences?: number;
  /**
   * Last permitted planned occurrence time, within one year of initial delivery.
   */
  end_at?: string;
  /**
   * Total attempts per retry cycle; one disables automatic retries.
   */
  max_attempts?: number;
  /**
   * Initial retry delay, doubled per failure up to one hour.
   */
  backoff_seconds?: number;
  /**
   * At most 4096 decoded bytes.
   */
  data_base64: string;
  binary?: boolean;
  metadata?: Record<string, string>;
  /**
   * Future time within 30 days of creation.
   */
  deliver_at: string;
};

