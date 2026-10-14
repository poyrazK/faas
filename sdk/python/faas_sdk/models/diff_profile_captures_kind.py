from typing import Literal

DiffProfileCapturesKind = Literal["cpu", "heap", "stacks"]

DIFF_PROFILE_CAPTURES_KIND_VALUES: set[DiffProfileCapturesKind] = {
    "cpu",
    "heap",
    "stacks",
}


def check_diff_profile_captures_kind(value: str) -> DiffProfileCapturesKind:
    if value in DIFF_PROFILE_CAPTURES_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DIFF_PROFILE_CAPTURES_KIND_VALUES!r}")
