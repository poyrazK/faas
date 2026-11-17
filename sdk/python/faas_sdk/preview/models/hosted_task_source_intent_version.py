from typing import Literal

HostedTaskSourceIntentVersion = Literal[1]

HOSTED_TASK_SOURCE_INTENT_VERSION_VALUES: set[HostedTaskSourceIntentVersion] = {
    1,
}


def check_hosted_task_source_intent_version(value: int) -> HostedTaskSourceIntentVersion:
    if value in HOSTED_TASK_SOURCE_INTENT_VERSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {HOSTED_TASK_SOURCE_INTENT_VERSION_VALUES!r}")
