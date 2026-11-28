from typing import Literal

EnvironmentGitOpsExplanationStatus = Literal["blocked", "degraded", "progressing", "reporting", "serving", "unknown"]

ENVIRONMENT_GIT_OPS_EXPLANATION_STATUS_VALUES: set[EnvironmentGitOpsExplanationStatus] = {
    "blocked",
    "degraded",
    "progressing",
    "reporting",
    "serving",
    "unknown",
}


def check_environment_git_ops_explanation_status(value: str) -> EnvironmentGitOpsExplanationStatus:
    if value in ENVIRONMENT_GIT_OPS_EXPLANATION_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ENVIRONMENT_GIT_OPS_EXPLANATION_STATUS_VALUES!r}")
