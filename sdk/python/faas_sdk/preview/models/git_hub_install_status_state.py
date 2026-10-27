from typing import Literal

GitHubInstallStatusState = Literal["bound", "installed", "not_installed"]

GIT_HUB_INSTALL_STATUS_STATE_VALUES: set[GitHubInstallStatusState] = {
    "bound",
    "installed",
    "not_installed",
}


def check_git_hub_install_status_state(value: str) -> GitHubInstallStatusState:
    if value in GIT_HUB_INSTALL_STATUS_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GIT_HUB_INSTALL_STATUS_STATE_VALUES!r}")
