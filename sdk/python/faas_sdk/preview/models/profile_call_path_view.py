from typing import Literal

ProfileCallPathView = Literal["candidate", "comparison"]

PROFILE_CALL_PATH_VIEW_VALUES: set[ProfileCallPathView] = {
    "candidate",
    "comparison",
}


def check_profile_call_path_view(value: str) -> ProfileCallPathView:
    if value in PROFILE_CALL_PATH_VIEW_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROFILE_CALL_PATH_VIEW_VALUES!r}")
