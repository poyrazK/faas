from typing import Literal

DiffChangeKind = Literal["add", "modify", "remove"]

DIFF_CHANGE_KIND_VALUES: set[DiffChangeKind] = {
    "add",
    "modify",
    "remove",
}


def check_diff_change_kind(value: str) -> DiffChangeKind:
    if value in DIFF_CHANGE_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DIFF_CHANGE_KIND_VALUES!r}")
