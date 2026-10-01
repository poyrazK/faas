from typing import Literal

SchedulePolicyOverlap = Literal["allow", "replace", "skip"]

SCHEDULE_POLICY_OVERLAP_VALUES: set[SchedulePolicyOverlap] = {
    "allow",
    "replace",
    "skip",
}


def check_schedule_policy_overlap(value: str) -> SchedulePolicyOverlap:
    if value in SCHEDULE_POLICY_OVERLAP_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCHEDULE_POLICY_OVERLAP_VALUES!r}")
