from typing import Literal

ReleaseFreezeKind = Literal["after_rollback", "weekly", "window"]

RELEASE_FREEZE_KIND_VALUES: set[ReleaseFreezeKind] = {
    "after_rollback",
    "weekly",
    "window",
}


def check_release_freeze_kind(value: str) -> ReleaseFreezeKind:
    if value in RELEASE_FREEZE_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {RELEASE_FREEZE_KIND_VALUES!r}")
