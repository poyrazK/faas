from typing import Literal

EnvironmentGitOpsExplanationReportStatus = Literal["blocked", "converged", "drifted", "failed", "overridden", "partial"]

ENVIRONMENT_GIT_OPS_EXPLANATION_REPORT_STATUS_VALUES: set[EnvironmentGitOpsExplanationReportStatus] = {
    "blocked",
    "converged",
    "drifted",
    "failed",
    "overridden",
    "partial",
}


def check_environment_git_ops_explanation_report_status(value: str) -> EnvironmentGitOpsExplanationReportStatus:
    if value in ENVIRONMENT_GIT_OPS_EXPLANATION_REPORT_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ENVIRONMENT_GIT_OPS_EXPLANATION_REPORT_STATUS_VALUES!r}"
    )
