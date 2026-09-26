from typing import Literal

ProjectEnvironmentResponsePreviewState = Literal["closed", "open", "tearing_down"]

PROJECT_ENVIRONMENT_RESPONSE_PREVIEW_STATE_VALUES: set[ProjectEnvironmentResponsePreviewState] = {
    "closed",
    "open",
    "tearing_down",
}


def check_project_environment_response_preview_state(value: str) -> ProjectEnvironmentResponsePreviewState:
    if value in PROJECT_ENVIRONMENT_RESPONSE_PREVIEW_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_RESPONSE_PREVIEW_STATE_VALUES!r}"
    )
