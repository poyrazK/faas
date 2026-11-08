from typing import Literal

GitHubAccountConnectionHealth = Literal["degraded", "healthy", "unknown"]

GIT_HUB_ACCOUNT_CONNECTION_HEALTH_VALUES: set[GitHubAccountConnectionHealth] = {
    "degraded",
    "healthy",
    "unknown",
}


def check_git_hub_account_connection_health(value: str) -> GitHubAccountConnectionHealth:
    if value in GIT_HUB_ACCOUNT_CONNECTION_HEALTH_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GIT_HUB_ACCOUNT_CONNECTION_HEALTH_VALUES!r}")
