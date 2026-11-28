from typing import Literal

HostedTaskSourceEntryMode = Literal[384, 448]

HOSTED_TASK_SOURCE_ENTRY_MODE_VALUES: set[HostedTaskSourceEntryMode] = {
    384,
    448,
}


def check_hosted_task_source_entry_mode(value: int) -> HostedTaskSourceEntryMode:
    if value in HOSTED_TASK_SOURCE_ENTRY_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {HOSTED_TASK_SOURCE_ENTRY_MODE_VALUES!r}")
