from typing import Literal

ApplicationStandardAssignmentScope = Literal["application", "organization", "project"]

APPLICATION_STANDARD_ASSIGNMENT_SCOPE_VALUES: set[ApplicationStandardAssignmentScope] = {
    "application",
    "organization",
    "project",
}


def check_application_standard_assignment_scope(value: str) -> ApplicationStandardAssignmentScope:
    if value in APPLICATION_STANDARD_ASSIGNMENT_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APPLICATION_STANDARD_ASSIGNMENT_SCOPE_VALUES!r}")
