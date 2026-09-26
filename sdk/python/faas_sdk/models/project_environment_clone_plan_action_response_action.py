from typing import Literal

ProjectEnvironmentClonePlanActionResponseAction = Literal[
    "copy", "copy_sealed", "isolate", "not_available", "promote", "reuse", "share", "shared", "skip"
]

PROJECT_ENVIRONMENT_CLONE_PLAN_ACTION_RESPONSE_ACTION_VALUES: set[ProjectEnvironmentClonePlanActionResponseAction] = {
    "copy",
    "copy_sealed",
    "isolate",
    "not_available",
    "promote",
    "reuse",
    "share",
    "shared",
    "skip",
}


def check_project_environment_clone_plan_action_response_action(
    value: str,
) -> ProjectEnvironmentClonePlanActionResponseAction:
    if value in PROJECT_ENVIRONMENT_CLONE_PLAN_ACTION_RESPONSE_ACTION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_CLONE_PLAN_ACTION_RESPONSE_ACTION_VALUES!r}"
    )
