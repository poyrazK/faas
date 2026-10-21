from typing import Literal

ControlOrgProjectReleaseAction = Literal["abort", "contract", "recover", "resolve-contract"]

CONTROL_ORG_PROJECT_RELEASE_ACTION_VALUES: set[ControlOrgProjectReleaseAction] = {
    "abort",
    "contract",
    "recover",
    "resolve-contract",
}


def check_control_org_project_release_action(value: str) -> ControlOrgProjectReleaseAction:
    if value in CONTROL_ORG_PROJECT_RELEASE_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CONTROL_ORG_PROJECT_RELEASE_ACTION_VALUES!r}")
