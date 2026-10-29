from typing import Literal

ObsRecordingCoverageLimitationsItem = Literal[
    "dependencies_are_expected_catalog_syntax_not_proven_impact",
    "expectation_selected_per_request_not_deployment_evidence",
    "group_intervals_offsets_annotations_and_source_paths_not_compared",
    "missing_recording_labels_are_empty_per_prometheus_wire_contract",
    "nonexact_selectors_duplicate_names_and_cycles_leave_uncertainty",
    "previews_bounded_totals_cover_complete_inputs",
    "raw_queries_labels_and_errors_excluded",
    "recording_definitions_not_evaluation_or_metric_health",
    "selected_set_is_a_minimum_not_an_exclusive_inventory",
]

OBS_RECORDING_COVERAGE_LIMITATIONS_ITEM_VALUES: set[ObsRecordingCoverageLimitationsItem] = {
    "dependencies_are_expected_catalog_syntax_not_proven_impact",
    "expectation_selected_per_request_not_deployment_evidence",
    "group_intervals_offsets_annotations_and_source_paths_not_compared",
    "missing_recording_labels_are_empty_per_prometheus_wire_contract",
    "nonexact_selectors_duplicate_names_and_cycles_leave_uncertainty",
    "previews_bounded_totals_cover_complete_inputs",
    "raw_queries_labels_and_errors_excluded",
    "recording_definitions_not_evaluation_or_metric_health",
    "selected_set_is_a_minimum_not_an_exclusive_inventory",
}


def check_obs_recording_coverage_limitations_item(value: str) -> ObsRecordingCoverageLimitationsItem:
    if value in OBS_RECORDING_COVERAGE_LIMITATIONS_ITEM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RECORDING_COVERAGE_LIMITATIONS_ITEM_VALUES!r}")
