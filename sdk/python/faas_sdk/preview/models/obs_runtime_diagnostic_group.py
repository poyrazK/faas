from typing import Literal

ObsRuntimeDiagnosticGroup = Literal[
    "app_task_health",
    "app_task_operations",
    "app_task_results",
    "build_recovery_health",
    "builder_health",
    "command_cron_health",
    "command_cron_outcomes",
    "command_cron_timing",
    "delayed_task_dispatch",
    "handoff_health",
    "http_cron_delivery",
    "post_capture_health",
    "preparation_health",
    "snapshot_prime_health",
]

OBS_RUNTIME_DIAGNOSTIC_GROUP_VALUES: set[ObsRuntimeDiagnosticGroup] = {
    "app_task_health",
    "app_task_operations",
    "app_task_results",
    "build_recovery_health",
    "builder_health",
    "command_cron_health",
    "command_cron_outcomes",
    "command_cron_timing",
    "delayed_task_dispatch",
    "handoff_health",
    "http_cron_delivery",
    "post_capture_health",
    "preparation_health",
    "snapshot_prime_health",
}


def check_obs_runtime_diagnostic_group(value: str) -> ObsRuntimeDiagnosticGroup:
    if value in OBS_RUNTIME_DIAGNOSTIC_GROUP_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_DIAGNOSTIC_GROUP_VALUES!r}")
