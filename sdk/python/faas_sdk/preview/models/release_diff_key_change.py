from typing import Literal

ReleaseDiffKeyChange = Literal["added", "changed", "removed"]

RELEASE_DIFF_KEY_CHANGE_VALUES: set[ReleaseDiffKeyChange] = {
    "added",
    "changed",
    "removed",
}


def check_release_diff_key_change(value: str) -> ReleaseDiffKeyChange:
    if value in RELEASE_DIFF_KEY_CHANGE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {RELEASE_DIFF_KEY_CHANGE_VALUES!r}")
