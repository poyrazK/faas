from typing import Literal

ObsRuntimeCommandCronClassName = Literal[
    "account_held",
    "deployment_missing",
    "disabled",
    "due",
    "exclusive_due",
    "not_due",
    "overlap_due",
    "replacement_wait",
    "suspended",
    "unknown",
]

OBS_RUNTIME_COMMAND_CRON_CLASS_NAME_VALUES: set[ObsRuntimeCommandCronClassName] = {
    "account_held",
    "deployment_missing",
    "disabled",
    "due",
    "exclusive_due",
    "not_due",
    "overlap_due",
    "replacement_wait",
    "suspended",
    "unknown",
}


def check_obs_runtime_command_cron_class_name(value: str) -> ObsRuntimeCommandCronClassName:
    if value in OBS_RUNTIME_COMMAND_CRON_CLASS_NAME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_COMMAND_CRON_CLASS_NAME_VALUES!r}")
