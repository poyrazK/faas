from typing import Literal

ObsRuntimeHandoffEvidenceReason = Literal[
    "collection_before_boundary",
    "collection_failed",
    "collection_not_observed",
    "collection_stale",
    "conflicting_sample_times",
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

OBS_RUNTIME_HANDOFF_EVIDENCE_REASON_VALUES: set[ObsRuntimeHandoffEvidenceReason] = {
    "collection_before_boundary",
    "collection_failed",
    "collection_not_observed",
    "collection_stale",
    "conflicting_sample_times",
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


def check_obs_runtime_handoff_evidence_reason(value: str) -> ObsRuntimeHandoffEvidenceReason:
    if value in OBS_RUNTIME_HANDOFF_EVIDENCE_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_HANDOFF_EVIDENCE_REASON_VALUES!r}")
