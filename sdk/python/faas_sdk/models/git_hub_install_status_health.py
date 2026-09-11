from typing import Literal

GitHubInstallStatusHealth = Literal["degraded", "healthy", "not_connected", "unknown"]

GIT_HUB_INSTALL_STATUS_HEALTH_VALUES: set[GitHubInstallStatusHealth] = {
    "degraded",
    "healthy",
    "not_connected",
    "unknown",
}


def check_git_hub_install_status_health(value: str) -> GitHubInstallStatusHealth:
    if value in GIT_HUB_INSTALL_STATUS_HEALTH_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GIT_HUB_INSTALL_STATUS_HEALTH_VALUES!r}")
