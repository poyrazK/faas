from typing import Literal

EnvironmentGitPromotionOriginProvider = Literal["github", "gitlab"]

ENVIRONMENT_GIT_PROMOTION_ORIGIN_PROVIDER_VALUES: set[EnvironmentGitPromotionOriginProvider] = {
    "github",
    "gitlab",
}


def check_environment_git_promotion_origin_provider(value: str) -> EnvironmentGitPromotionOriginProvider:
    if value in ENVIRONMENT_GIT_PROMOTION_ORIGIN_PROVIDER_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ENVIRONMENT_GIT_PROMOTION_ORIGIN_PROVIDER_VALUES!r}")
