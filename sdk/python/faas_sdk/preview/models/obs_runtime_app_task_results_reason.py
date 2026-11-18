from typing import Literal

ObsRuntimeAppTaskResultsReason = Literal[
    "comparison_not_requested", "incomplete_result_evidence", "result_evidence_unavailable", "result_increases_observed"
]

OBS_RUNTIME_APP_TASK_RESULTS_REASON_VALUES: set[ObsRuntimeAppTaskResultsReason] = {
    "comparison_not_requested",
    "incomplete_result_evidence",
    "result_evidence_unavailable",
    "result_increases_observed",
}


def check_obs_runtime_app_task_results_reason(value: str) -> ObsRuntimeAppTaskResultsReason:
    if value in OBS_RUNTIME_APP_TASK_RESULTS_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_APP_TASK_RESULTS_REASON_VALUES!r}")
