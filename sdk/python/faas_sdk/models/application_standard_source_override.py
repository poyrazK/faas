from typing import Literal

ApplicationStandardSourceOverride = Literal["extend", "narrow", "none"]

APPLICATION_STANDARD_SOURCE_OVERRIDE_VALUES: set[ApplicationStandardSourceOverride] = {
    "extend",
    "narrow",
    "none",
}


def check_application_standard_source_override(value: str) -> ApplicationStandardSourceOverride:
    if value in APPLICATION_STANDARD_SOURCE_OVERRIDE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APPLICATION_STANDARD_SOURCE_OVERRIDE_VALUES!r}")
