from typing import Literal

EnvironmentGitOpsExplanationReportFreshness = Literal["fresh", "stale", "unknown"]

ENVIRONMENT_GIT_OPS_EXPLANATION_REPORT_FRESHNESS_VALUES: set[EnvironmentGitOpsExplanationReportFreshness] = {
    "fresh",
    "stale",
    "unknown",
}


def check_environment_git_ops_explanation_report_freshness(value: str) -> EnvironmentGitOpsExplanationReportFreshness:
    if value in ENVIRONMENT_GIT_OPS_EXPLANATION_REPORT_FRESHNESS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ENVIRONMENT_GIT_OPS_EXPLANATION_REPORT_FRESHNESS_VALUES!r}"
    )
