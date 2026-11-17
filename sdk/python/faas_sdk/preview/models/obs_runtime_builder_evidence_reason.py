from typing import Literal

ObsRuntimeBuilderEvidenceReason = Literal[
    "conflicting_series",
    "conflicting_targets",
    "expected_target_missing",
    "invalid_evidence",
    "invalid_sample_time",
    "invalid_value",
    "metric_before_boundary",
    "metric_missing",
    "metric_stale",
    "scrape_before_boundary",
    "scrape_down",
    "scrape_stale",
    "source_unavailable",
]

OBS_RUNTIME_BUILDER_EVIDENCE_REASON_VALUES: set[ObsRuntimeBuilderEvidenceReason] = {
    "conflicting_series",
    "conflicting_targets",
    "expected_target_missing",
    "invalid_evidence",
    "invalid_sample_time",
    "invalid_value",
    "metric_before_boundary",
    "metric_missing",
    "metric_stale",
    "scrape_before_boundary",
    "scrape_down",
    "scrape_stale",
    "source_unavailable",
}


def check_obs_runtime_builder_evidence_reason(value: str) -> ObsRuntimeBuilderEvidenceReason:
    if value in OBS_RUNTIME_BUILDER_EVIDENCE_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_BUILDER_EVIDENCE_REASON_VALUES!r}")
