from typing import Literal

EnvironmentGitOpsExplanationSourceFreshness = Literal["fresh", "stale", "unknown"]

ENVIRONMENT_GIT_OPS_EXPLANATION_SOURCE_FRESHNESS_VALUES: set[EnvironmentGitOpsExplanationSourceFreshness] = {
    "fresh",
    "stale",
    "unknown",
}


def check_environment_git_ops_explanation_source_freshness(value: str) -> EnvironmentGitOpsExplanationSourceFreshness:
    if value in ENVIRONMENT_GIT_OPS_EXPLANATION_SOURCE_FRESHNESS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ENVIRONMENT_GIT_OPS_EXPLANATION_SOURCE_FRESHNESS_VALUES!r}"
    )
