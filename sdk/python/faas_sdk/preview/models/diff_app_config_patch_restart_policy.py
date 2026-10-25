from typing import Literal

DiffAppConfigPatchRestartPolicy = Literal["always", "no", "on-failure", "unless-stopped"]

DIFF_APP_CONFIG_PATCH_RESTART_POLICY_VALUES: set[DiffAppConfigPatchRestartPolicy] = {
    "always",
    "no",
    "on-failure",
    "unless-stopped",
}


def check_diff_app_config_patch_restart_policy(value: str) -> DiffAppConfigPatchRestartPolicy:
    if value in DIFF_APP_CONFIG_PATCH_RESTART_POLICY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DIFF_APP_CONFIG_PATCH_RESTART_POLICY_VALUES!r}")
