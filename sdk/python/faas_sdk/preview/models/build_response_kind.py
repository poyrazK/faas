from typing import Literal

BuildResponseKind = Literal["dockerfile", "github", "railpack", "tarball"]

BUILD_RESPONSE_KIND_VALUES: set[BuildResponseKind] = {
    "dockerfile",
    "github",
    "railpack",
    "tarball",
}


def check_build_response_kind(value: str) -> BuildResponseKind:
    if value in BUILD_RESPONSE_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {BUILD_RESPONSE_KIND_VALUES!r}")
