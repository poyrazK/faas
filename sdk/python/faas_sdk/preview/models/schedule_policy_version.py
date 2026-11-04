from typing import Literal

SchedulePolicyVersion = Literal[1]

SCHEDULE_POLICY_VERSION_VALUES: set[SchedulePolicyVersion] = {
    1,
}


def check_schedule_policy_version(value: int) -> SchedulePolicyVersion:
    if value in SCHEDULE_POLICY_VERSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCHEDULE_POLICY_VERSION_VALUES!r}")
