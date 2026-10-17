from typing import Literal

ControlProjectReleaseAction = Literal["abort", "contract", "recover", "resolve-contract"]

CONTROL_PROJECT_RELEASE_ACTION_VALUES: set[ControlProjectReleaseAction] = {
    "abort",
    "contract",
    "recover",
    "resolve-contract",
}


def check_control_project_release_action(value: str) -> ControlProjectReleaseAction:
    if value in CONTROL_PROJECT_RELEASE_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CONTROL_PROJECT_RELEASE_ACTION_VALUES!r}")
