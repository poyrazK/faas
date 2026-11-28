from typing import Literal

TaskToolReleaseAppRole = Literal["observer", "previous_worker", "web", "worker"]

TASK_TOOL_RELEASE_APP_ROLE_VALUES: set[TaskToolReleaseAppRole] = {
    "observer",
    "previous_worker",
    "web",
    "worker",
}


def check_task_tool_release_app_role(value: str) -> TaskToolReleaseAppRole:
    if value in TASK_TOOL_RELEASE_APP_ROLE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TASK_TOOL_RELEASE_APP_ROLE_VALUES!r}")
