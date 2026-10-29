from typing import Literal

ProjectReleaseVerificationActionsItem = Literal["abort", "recover"]

PROJECT_RELEASE_VERIFICATION_ACTIONS_ITEM_VALUES: set[ProjectReleaseVerificationActionsItem] = {
    "abort",
    "recover",
}


def check_project_release_verification_actions_item(value: str) -> ProjectReleaseVerificationActionsItem:
    if value in PROJECT_RELEASE_VERIFICATION_ACTIONS_ITEM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROJECT_RELEASE_VERIFICATION_ACTIONS_ITEM_VALUES!r}")
