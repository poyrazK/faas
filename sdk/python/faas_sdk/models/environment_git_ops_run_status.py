from typing import Literal

EnvironmentGitOpsRunStatus = Literal[
    "blocked", "converged", "drifted", "failed", "overridden", "partial", "running", "superseded"
]

ENVIRONMENT_GIT_OPS_RUN_STATUS_VALUES: set[EnvironmentGitOpsRunStatus] = {
    "blocked",
    "converged",
    "drifted",
    "failed",
    "overridden",
    "partial",
    "running",
    "superseded",
}


def check_environment_git_ops_run_status(value: str) -> EnvironmentGitOpsRunStatus:
    if value in ENVIRONMENT_GIT_OPS_RUN_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ENVIRONMENT_GIT_OPS_RUN_STATUS_VALUES!r}")
