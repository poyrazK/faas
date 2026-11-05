from typing import Literal

ReleaseFreezeSource = Literal["automatic_rollback", "manual"]

RELEASE_FREEZE_SOURCE_VALUES: set[ReleaseFreezeSource] = {
    "automatic_rollback",
    "manual",
}


def check_release_freeze_source(value: str) -> ReleaseFreezeSource:
    if value in RELEASE_FREEZE_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {RELEASE_FREEZE_SOURCE_VALUES!r}")
