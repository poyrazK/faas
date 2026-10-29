from typing import Literal

ProjectReleaseControlAction = Literal["abort", "recover"]

PROJECT_RELEASE_CONTROL_ACTION_VALUES: set[ProjectReleaseControlAction] = {
    "abort",
    "recover",
}


def check_project_release_control_action(value: str) -> ProjectReleaseControlAction:
    if value in PROJECT_RELEASE_CONTROL_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROJECT_RELEASE_CONTROL_ACTION_VALUES!r}")
