from typing import Literal

ResourceProfile = Literal["large", "medium", "micro", "small", "xlarge"]

RESOURCE_PROFILE_VALUES: set[ResourceProfile] = {
    "large",
    "medium",
    "micro",
    "small",
    "xlarge",
}


def check_resource_profile(value: str) -> ResourceProfile:
    if value in RESOURCE_PROFILE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {RESOURCE_PROFILE_VALUES!r}")
