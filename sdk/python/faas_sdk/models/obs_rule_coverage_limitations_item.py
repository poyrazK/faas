from typing import Literal

ObsRuleCoverageLimitationsItem = Literal[
    "alert_rules_only_recording_rules_not_checked",
    "canonical_syntax_not_semantic_equivalence",
    "coverage_not_evaluation_metric_or_notification_health",
    "expectation_selected_per_request_not_deployment_evidence",
    "group_intervals_offsets_annotations_and_source_paths_not_compared",
    "preview_bounded_totals_cover_complete_source",
    "raw_queries_labels_and_errors_excluded",
    "selected_set_is_a_minimum_not_an_exclusive_inventory",
]

OBS_RULE_COVERAGE_LIMITATIONS_ITEM_VALUES: set[ObsRuleCoverageLimitationsItem] = {
    "alert_rules_only_recording_rules_not_checked",
    "canonical_syntax_not_semantic_equivalence",
    "coverage_not_evaluation_metric_or_notification_health",
    "expectation_selected_per_request_not_deployment_evidence",
    "group_intervals_offsets_annotations_and_source_paths_not_compared",
    "preview_bounded_totals_cover_complete_source",
    "raw_queries_labels_and_errors_excluded",
    "selected_set_is_a_minimum_not_an_exclusive_inventory",
}


def check_obs_rule_coverage_limitations_item(value: str) -> ObsRuleCoverageLimitationsItem:
    if value in OBS_RULE_COVERAGE_LIMITATIONS_ITEM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RULE_COVERAGE_LIMITATIONS_ITEM_VALUES!r}")
