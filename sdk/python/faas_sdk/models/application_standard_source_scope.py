from typing import Literal

ApplicationStandardSourceScope = Literal["application", "organization", "project"]

APPLICATION_STANDARD_SOURCE_SCOPE_VALUES: set[ApplicationStandardSourceScope] = {
    "application",
    "organization",
    "project",
}


def check_application_standard_source_scope(value: str) -> ApplicationStandardSourceScope:
    if value in APPLICATION_STANDARD_SOURCE_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APPLICATION_STANDARD_SOURCE_SCOPE_VALUES!r}")
