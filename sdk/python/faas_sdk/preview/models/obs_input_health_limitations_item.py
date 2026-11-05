from typing import Literal

ObsInputHealthLimitationsItem = Literal[
    "default_scrape_job_collision_uses_exported_job",
    "discovery_counts_not_target_identity_membership",
    "historical_samples_bounded_by_lookback",
    "metric_checks_cover_fresh_up_targets_only",
    "one_vetted_idle_safe_gauge_per_selected_job_not_all_rule_inputs",
    "raw_labels_queries_urls_and_errors_excluded",
    "readiness_sample_presence_not_readiness_value",
    "role_selected_per_request_not_deployment_evidence",
    "static_expectations_are_minimum_one_not_full_inventory",
]

OBS_INPUT_HEALTH_LIMITATIONS_ITEM_VALUES: set[ObsInputHealthLimitationsItem] = {
    "default_scrape_job_collision_uses_exported_job",
    "discovery_counts_not_target_identity_membership",
    "historical_samples_bounded_by_lookback",
    "metric_checks_cover_fresh_up_targets_only",
    "one_vetted_idle_safe_gauge_per_selected_job_not_all_rule_inputs",
    "raw_labels_queries_urls_and_errors_excluded",
    "readiness_sample_presence_not_readiness_value",
    "role_selected_per_request_not_deployment_evidence",
    "static_expectations_are_minimum_one_not_full_inventory",
}


def check_obs_input_health_limitations_item(value: str) -> ObsInputHealthLimitationsItem:
    if value in OBS_INPUT_HEALTH_LIMITATIONS_ITEM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_INPUT_HEALTH_LIMITATIONS_ITEM_VALUES!r}")
