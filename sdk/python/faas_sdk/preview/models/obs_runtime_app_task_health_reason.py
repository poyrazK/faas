from typing import Literal

ObsRuntimeAppTaskHealthReason = Literal[
    "app_task_evidence_observed", "app_task_evidence_unavailable", "incomplete_app_task_evidence"
]

OBS_RUNTIME_APP_TASK_HEALTH_REASON_VALUES: set[ObsRuntimeAppTaskHealthReason] = {
    "app_task_evidence_observed",
    "app_task_evidence_unavailable",
    "incomplete_app_task_evidence",
}


def check_obs_runtime_app_task_health_reason(value: str) -> ObsRuntimeAppTaskHealthReason:
    if value in OBS_RUNTIME_APP_TASK_HEALTH_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_APP_TASK_HEALTH_REASON_VALUES!r}")
