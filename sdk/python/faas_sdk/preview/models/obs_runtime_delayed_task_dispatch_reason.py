from typing import Literal

ObsRuntimeDelayedTaskDispatchReason = Literal[
    "comparison_not_requested",
    "delayed_task_dispatch_evidence_unavailable",
    "delayed_task_dispatch_increases_observed",
    "incomplete_delayed_task_dispatch_evidence",
]

OBS_RUNTIME_DELAYED_TASK_DISPATCH_REASON_VALUES: set[ObsRuntimeDelayedTaskDispatchReason] = {
    "comparison_not_requested",
    "delayed_task_dispatch_evidence_unavailable",
    "delayed_task_dispatch_increases_observed",
    "incomplete_delayed_task_dispatch_evidence",
}


def check_obs_runtime_delayed_task_dispatch_reason(value: str) -> ObsRuntimeDelayedTaskDispatchReason:
    if value in OBS_RUNTIME_DELAYED_TASK_DISPATCH_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_DELAYED_TASK_DISPATCH_REASON_VALUES!r}")
