from typing import Literal

ApplicationStandardSourceMode = Literal["default", "mandatory", "restricted"]

APPLICATION_STANDARD_SOURCE_MODE_VALUES: set[ApplicationStandardSourceMode] = {
    "default",
    "mandatory",
    "restricted",
}


def check_application_standard_source_mode(value: str) -> ApplicationStandardSourceMode:
    if value in APPLICATION_STANDARD_SOURCE_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APPLICATION_STANDARD_SOURCE_MODE_VALUES!r}")
