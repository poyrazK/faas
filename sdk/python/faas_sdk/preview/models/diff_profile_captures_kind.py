from typing import Literal

DiffProfileCapturesKind = Literal["block", "cpu", "heap", "mutex", "stacks"]

DIFF_PROFILE_CAPTURES_KIND_VALUES: set[DiffProfileCapturesKind] = {
    "block",
    "cpu",
    "heap",
    "mutex",
    "stacks",
}


def check_diff_profile_captures_kind(value: str) -> DiffProfileCapturesKind:
    if value in DIFF_PROFILE_CAPTURES_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DIFF_PROFILE_CAPTURES_KIND_VALUES!r}")
