from typing import Literal

PreviewEnvironmentGitPlanResponseProvider = Literal["github", "gitlab"]

PREVIEW_ENVIRONMENT_GIT_PLAN_RESPONSE_PROVIDER_VALUES: set[PreviewEnvironmentGitPlanResponseProvider] = {
    "github",
    "gitlab",
}


def check_preview_environment_git_plan_response_provider(value: str) -> PreviewEnvironmentGitPlanResponseProvider:
    if value in PREVIEW_ENVIRONMENT_GIT_PLAN_RESPONSE_PROVIDER_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PREVIEW_ENVIRONMENT_GIT_PLAN_RESPONSE_PROVIDER_VALUES!r}"
    )
