from typing import Literal

ObsInputMetricReason = Literal[
    "invalid_evidence",
    "metric_sample_gap",
    "no_fresh_up_targets",
    "source_unavailable",
    "target_evidence_unavailable",
    "zero_expected_targets",
]

OBS_INPUT_METRIC_REASON_VALUES: set[ObsInputMetricReason] = {
    "invalid_evidence",
    "metric_sample_gap",
    "no_fresh_up_targets",
    "source_unavailable",
    "target_evidence_unavailable",
    "zero_expected_targets",
}


def check_obs_input_metric_reason(value: str) -> ObsInputMetricReason:
    if value in OBS_INPUT_METRIC_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_INPUT_METRIC_REASON_VALUES!r}")
